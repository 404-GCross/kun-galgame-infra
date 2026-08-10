package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"api/internal/platform/image/model"
	"api/internal/platform/image/moderation"
	"api/internal/platform/image/storage"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type ModerationWorker struct {
	db      *gorm.DB
	storage *storage.Client
	prov    moderation.Provider

	MaxAttempts int
	BatchSize   int
	PollEvery   time.Duration
}

func NewModerationWorker(db *gorm.DB, s *storage.Client, p moderation.Provider) *ModerationWorker {
	return &ModerationWorker{
		db: db, storage: s, prov: p,
		MaxAttempts: 5, BatchSize: 20, PollEvery: 15 * time.Second,
	}
}

func (w *ModerationWorker) Run(ctx context.Context) error {
	slog.Info("moderation worker starting",
		"provider", w.prov.Name(),
		"batch", w.BatchSize,
		"poll_interval", w.PollEvery.String(),
	)

	t := time.NewTicker(w.PollEvery)
	defer t.Stop()

	w.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			w.runOnce(ctx)
		}
	}
}

func (w *ModerationWorker) runOnce(ctx context.Context) {
	rows, err := w.claim(ctx)
	if err != nil {
		slog.Error("claim queue failed", "err", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	slog.Info("processing moderation batch", "count", len(rows))
	for _, q := range rows {
		if err := w.processOne(ctx, q); err != nil {
			slog.Error("process moderation", "hash", q.Hash, "err", err)
		}
	}
}

func (w *ModerationWorker) claim(ctx context.Context) ([]model.ModerationQueue, error) {
	var claimed []model.ModerationQueue
	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		staleBefore := time.Now().Add(-5 * time.Minute)

		rows := []model.ModerationQueue{}
		if err := tx.
			Clauses().
			Raw(`
				SELECT * FROM image_moderation_queue
				WHERE pickup_at IS NULL OR pickup_at < ?
				ORDER BY enqueued_at ASC
				LIMIT ?
				FOR UPDATE SKIP LOCKED
			`, staleBefore, w.BatchSize).
			Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		now := time.Now()
		ids := make([]int64, 0, len(rows))
		for i := range rows {
			rows[i].PickupAt = &now
			ids = append(ids, rows[i].ID)
		}
		if err := tx.Model(&model.ModerationQueue{}).
			Where("id IN ?", ids).
			Update("pickup_at", now).Error; err != nil {
			return err
		}
		claimed = rows
		return nil
	})
	return claimed, err
}

func (w *ModerationWorker) processOne(ctx context.Context, q model.ModerationQueue) error {
	var img model.Image
	if err := w.db.WithContext(ctx).
		Where("hash = ?", q.Hash).
		First(&img).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return w.dropQueue(ctx, q.ID)
		}
		return w.failAttempt(ctx, q.ID, err)
	}

	rc, err := w.storage.Get(ctx, img.StorageKey)
	if err != nil {
		return w.failAttempt(ctx, q.ID, fmt.Errorf("fetch body: %w", err))
	}
	body, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return w.failAttempt(ctx, q.ID, fmt.Errorf("read body: %w", err))
	}

	dec, err := w.prov.AsyncCheck(ctx, body, img.MIME)
	if err != nil {
		return w.failAttempt(ctx, q.ID, fmt.Errorf("provider: %w", err))
	}

	if err := w.applyDecision(ctx, &img, dec); err != nil {
		return w.failAttempt(ctx, q.ID, fmt.Errorf("apply decision: %w", err))
	}

	return w.dropQueue(ctx, q.ID)
}

func (w *ModerationWorker) applyDecision(ctx context.Context, img *model.Image, dec *moderation.Decision) error {
	status := model.ReviewApproved
	switch dec.Verdict {
	case moderation.VerdictReject:
		status = model.ReviewRejected
	case moderation.VerdictReview:
		status = model.ReviewManual
	case moderation.VerdictApprove:
		status = model.ReviewApproved
	default:
		status = model.ReviewPending
	}

	now := time.Now()
	updates := map[string]any{
		"review_status": status,
		"reviewed_at":   now,
	}
	if dec.Labels != nil {
		if raw, err := json.Marshal(dec.Labels); err == nil {
			updates["review_labels"] = datatypes.JSON(raw)
		}
	}

	return w.db.WithContext(ctx).
		Model(&model.Image{}).
		Where("id = ?", img.ID).
		Updates(updates).Error
}

func (w *ModerationWorker) dropQueue(ctx context.Context, id int64) error {
	return w.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&model.ModerationQueue{}).Error
}

func (w *ModerationWorker) failAttempt(ctx context.Context, id int64, cause error) error {
	var q model.ModerationQueue
	if err := w.db.WithContext(ctx).First(&q, id).Error; err != nil {
		return err
	}
	q.Attempts++
	q.LastError = cause.Error()

	if q.Attempts >= w.MaxAttempts {
		slog.Warn("moderation max attempts reached; escalating to manual",
			"hash", q.Hash, "attempts", q.Attempts, "err", cause)
		if err := w.db.WithContext(ctx).
			Model(&model.Image{}).
			Where("hash = ?", q.Hash).
			Update("review_status", model.ReviewManual).Error; err != nil {
			slog.Error("escalate to manual failed", "hash", q.Hash, "err", err)
		}
		return w.dropQueue(ctx, id)
	}

	return w.db.WithContext(ctx).
		Model(&model.ModerationQueue{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"attempts":   q.Attempts,
			"last_error": q.LastError,
			"pickup_at":  nil,
		}).Error
}
