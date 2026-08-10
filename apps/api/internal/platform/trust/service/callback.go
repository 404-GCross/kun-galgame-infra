package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CallbackWorker struct {
	db          *gorm.DB
	httpClient  *http.Client
	backoff     []time.Duration
	maxAttempts int
	batchSize   int
	interval    time.Duration
	now         func() time.Time
}

func NewCallbackWorker(db *gorm.DB) *CallbackWorker {
	return &CallbackWorker{
		db:          db,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		backoff:     callbackBackoff,
		maxAttempts: callbackMaxAttempts,
		batchSize:   20,
		interval:    5 * time.Second,
		now:         time.Now,
	}
}

type callbackBody struct {
	DispositionID int64  `json:"disposition_id"`
	SubjectKind   string `json:"subject_kind"`
	SubjectID     string `json:"subject_id"`
	Action        int16  `json:"action"`
	ReasonCode    string `json:"reason_code"`
}

func SignPayload(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (w *CallbackWorker) Run(ctx context.Context) {
	slog.Info("trust callback worker starting", "interval", w.interval.String(), "batch", w.batchSize)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		if _, err := w.DeliverDue(ctx); err != nil {
			slog.Error("trust callback deliver", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *CallbackWorker) DeliverDue(ctx context.Context) (int, error) {
	processed := 0
	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var due []model.TrustDisposition
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("callback_status = ? AND next_attempt_at <= ?", model.CallbackStatusPending, w.now()).
			Order("id ASC").Limit(w.batchSize).Find(&due).Error; err != nil {
			return err
		}
		for i := range due {
			if err := w.deliverOne(ctx, tx, &due[i]); err != nil {
				return err
			}
			processed++
		}
		return nil
	})
	return processed, err
}

func (w *CallbackWorker) deliverOne(ctx context.Context, tx *gorm.DB, d *model.TrustDisposition) error {
	var item model.TrustReviewItem
	if err := tx.Take(&item, d.ReviewItemID).Error; err != nil {
		return err
	}
	var kind model.TrustSubjectKind
	kerr := tx.Where("site = ? AND key = ?", item.Site, item.SubjectKind).Take(&kind).Error
	if kerr != nil && !errors.Is(kerr, gorm.ErrRecordNotFound) {
		return kerr
	}

	ok := false
	if kind.CallbackURL != nil && *kind.CallbackURL != "" {
		ok = w.post(ctx, *kind.CallbackURL, secretOf(kind), item, d)
	}
	if ok {
		return w.markDelivered(tx, d, item)
	}
	return w.markFailure(tx, d, item)
}

func (w *CallbackWorker) post(ctx context.Context, url, secret string, item model.TrustReviewItem, d *model.TrustDisposition) bool {
	body, err := json.Marshal(callbackBody{
		DispositionID: d.ID, SubjectKind: item.SubjectKind, SubjectID: item.SubjectID,
		Action: d.Action, ReasonCode: d.ReasonCode,
	})
	if err != nil {
		return false
	}
	ts := strconv.FormatInt(w.now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Trust-Timestamp", ts)
	req.Header.Set("X-Trust-Signature", SignPayload(secret, ts, body))
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func (w *CallbackWorker) markDelivered(tx *gorm.DB, d *model.TrustDisposition, item model.TrustReviewItem) error {
	now := w.now()
	if err := tx.Model(&model.TrustDisposition{}).Where("id = ?", d.ID).Updates(map[string]any{
		"callback_status":   model.CallbackStatusDelivered,
		"callback_attempts": d.CallbackAttempts + 1,
		"delivered_at":      now,
		"next_attempt_at":   nil,
	}).Error; err != nil {
		return err
	}
	return AppendAudit(tx, AuditEntry{
		Action: "callback_delivered",
		Site:   strptr(item.Site), SubjectKind: strptr(item.SubjectKind), SubjectID: strptr(item.SubjectID),
		ReasonCode: strptr(d.ReasonCode),
	})
}

func (w *CallbackWorker) markFailure(tx *gorm.DB, d *model.TrustDisposition, item model.TrustReviewItem) error {
	attempts := d.CallbackAttempts + 1
	if int(attempts) >= w.maxAttempts {
		if err := tx.Model(&model.TrustDisposition{}).Where("id = ?", d.ID).Updates(map[string]any{
			"callback_status":   model.CallbackStatusDeadLetter,
			"callback_attempts": attempts,
			"next_attempt_at":   nil,
		}).Error; err != nil {
			return err
		}
		return AppendAudit(tx, AuditEntry{
			Action: "callback_dead_letter",
			Site:   strptr(item.Site), SubjectKind: strptr(item.SubjectKind), SubjectID: strptr(item.SubjectID),
			ReasonCode: strptr(d.ReasonCode),
		})
	}
	next := w.now().Add(w.backoffFor(int(attempts)))
	return tx.Model(&model.TrustDisposition{}).Where("id = ?", d.ID).Updates(map[string]any{
		"callback_attempts": attempts,
		"next_attempt_at":   next,
	}).Error
}

func (w *CallbackWorker) backoffFor(k int) time.Duration {
	if len(w.backoff) == 0 {
		return 0
	}
	if k-1 < len(w.backoff) {
		return w.backoff[k-1]
	}
	return w.backoff[len(w.backoff)-1]
}

func secretOf(kind model.TrustSubjectKind) string {
	if kind.CallbackSecret == nil {
		return ""
	}
	return *kind.CallbackSecret
}
