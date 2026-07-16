package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"api/internal/platform/trust/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ScanWorker is the AI shadow-scoring pipeline (doc 18 §6; step 03). A ticker
// claims pending trust_scan_result rows with FOR UPDATE SKIP LOCKED (mirroring
// the callback worker) and scores each via the AI gateway's moderate-text route,
// driving it to a TERMINAL state:
//
//   - scored   — the gateway returned a verdict → flagged/score/categories/
//     channel/scored_at recorded.
//   - degraded — the gateway is unconfigured (env empty) / returned degraded:true
//     / the call failed → status=degraded, no verdict fields.
//
// Because the worker only ever claims pending, every row DRAINS to a terminal
// state and the queue never backs up unbounded — even with no channel layer.
//
// SHADOW MODE (mode=0, hardcoded this step): the worker records a verdict and
// NOTHING else. It NEVER creates a review item, NEVER emits a callback, NEVER
// enforces. Feeding the inbox (enqueue/live) is P2.
type ScanWorker struct {
	db        *gorm.DB
	gateway   ScanGateway
	batchSize int
	interval  time.Duration
	now       func() time.Time
}

// NewScanWorker builds the worker with production defaults.
func NewScanWorker(db *gorm.DB, gateway ScanGateway) *ScanWorker {
	return &ScanWorker{
		db:        db,
		gateway:   gateway,
		batchSize: scanBatchSize,
		interval:  scanInterval,
		now:       time.Now,
	}
}

// Run drives ScorePending on the worker interval until ctx is cancelled. Errors
// are logged, never fatal — the loop keeps running.
func (w *ScanWorker) Run(ctx context.Context) {
	slog.Info("trust scan worker starting",
		"interval", w.interval.String(), "batch", w.batchSize, "gateway_configured", w.gateway.Configured())
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		if _, err := w.ScorePending(ctx); err != nil {
			slog.Error("trust scan scoring", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ScorePending claims up to batchSize pending rows and scores each. It returns
// the number processed. A single transaction spans the claim + gateway call +
// terminal update per batch; SKIP LOCKED guarantees no concurrent worker picks
// the same rows, so no row is ever double-scored (低-volume v0 — doc 18 §0).
func (w *ScanWorker) ScorePending(ctx context.Context) (int, error) {
	processed := 0
	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pending []model.TrustScanResult
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", model.ScanStatusPending).
			Order("id ASC").Limit(w.batchSize).Find(&pending).Error; err != nil {
			return err
		}
		for i := range pending {
			if err := w.scoreOne(ctx, tx, &pending[i]); err != nil {
				return err
			}
			processed++
		}
		return nil
	})
	return processed, err
}

// scoreOne scores a single claimed row and records its terminal state within tx.
// Shadow mode: the ONLY side effect is an UPDATE to this row's own scan result —
// never a review item, callback, or enforcement action.
func (w *ScanWorker) scoreOne(ctx context.Context, tx *gorm.DB, r *model.TrustScanResult) error {
	// Env empty → drain without a network call (fail-closed: no unbounded backlog).
	if !w.gateway.Configured() {
		return w.markDegraded(tx, r)
	}
	verdict, err := w.gateway.Moderate(ctx, r.ContentText, r.AuthorID)
	if err != nil {
		slog.Warn("trust scan gateway call failed; draining to degraded", "scan_id", r.ID, "err", err)
		return w.markDegraded(tx, r)
	}
	if verdict.Degraded {
		// The gateway reported its own fail-open path (upstream down / over budget).
		return w.markDegraded(tx, r)
	}
	return w.markScored(tx, r, verdict)
}

// markScored records a gateway verdict: status=scored plus the verdict fields.
// Uses a map update so a false `flagged` is written explicitly (a scored,
// not-flagged row is flagged=false, distinct from a pending/degraded NULL). A
// flagged verdict logs one info line — a shadow-period observation surface.
func (w *ScanWorker) markScored(tx *gorm.DB, r *model.TrustScanResult, v GatewayVerdict) error {
	updates := map[string]any{
		"status":    model.ScanStatusScored,
		"flagged":   v.Flagged,
		"channel":   v.Channel,
		"scored_at": w.now(),
	}
	if v.Score != nil {
		updates["score"] = *v.Score
	}
	if len(v.Categories) > 0 {
		b, err := json.Marshal(v.Categories)
		if err != nil {
			return err
		}
		updates["categories"] = datatypes.JSON(b)
	}
	if v.Flagged {
		slog.Info("trust scan flagged (shadow)",
			"scan_id", r.ID, "site", r.Site, "subject_kind", r.SubjectKind, "subject_id", r.SubjectID,
			"channel", v.Channel, "categories", v.Categories)
	}
	return tx.Model(&model.TrustScanResult{}).Where("id = ?", r.ID).Updates(updates).Error
}

// markDegraded records a drain: only status flips to degraded. The verdict fields
// stay at their pending values (flagged/score/categories/scored_at NULL, channel
// ”), so a degraded row reads as "processed, no verdict". Terminal — the worker
// never re-claims it (env-ready powers on for NEW rows, not a re-scan of drained
// ones, this step).
func (w *ScanWorker) markDegraded(tx *gorm.DB, r *model.TrustScanResult) error {
	return tx.Model(&model.TrustScanResult{}).Where("id = ?", r.ID).
		Update("status", model.ScanStatusDegraded).Error
}
