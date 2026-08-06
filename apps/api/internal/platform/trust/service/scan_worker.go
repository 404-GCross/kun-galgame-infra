package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
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
//
// TIER0 (step 05): before the gateway call the worker runs the deterministic
// word-list matcher over content_text and records the match list into
// tier0_matched. This is PURE RECORDING — it never changes status semantics
// (scored/degraded stays gateway-driven) and never enqueues (the shadow
// invariant is untouched). It runs even on the env-empty degraded drain.
//
// LIVE MODE (wave 07, doc 18 §6 P2): with mode=ScanModeLive a FLAGGED verdict
// additionally opens an ai_text review item and queues a hide disposition
// (enforceFlagged), inside the same transaction as the verdict — so a subject is
// never left scored-but-unenforced by a crash between the two. Shadow behaviour
// is byte-for-byte unchanged; the mode is startup configuration, never per-row.
type ScanWorker struct {
	db         *gorm.DB
	gateway    ScanGateway
	tier0      Tier0Matcher
	mode       int16
	sampleRate float64
	rand       func() float64
	batchSize  int
	interval   time.Duration
	now        func() time.Time
}

// Tier0Matcher is the scan worker's SOLE contact with the Tier0 word list: given
// a resolved site and text it returns the matched normalized terms (never nil;
// [] = evaluated, no match). *TermService satisfies it; tests fake it.
type Tier0Matcher interface {
	Tier0Matches(ctx context.Context, site, text string) ([]string, error)
}

// NewScanWorker builds the worker with production defaults, in shadow mode.
// tier0 may be nil (then tier0_matched is left NULL = not evaluated). Shadow is
// the default so that merely deploying wave 07 changes nothing: enforcement is
// opted into explicitly via WithScanMode / KUN_TRUST_SCAN_MODE.
func NewScanWorker(db *gorm.DB, gateway ScanGateway, tier0 Tier0Matcher, opts ...ScanWorkerOption) *ScanWorker {
	w := &ScanWorker{
		db:      db,
		gateway: gateway,
		tier0:   tier0,
		mode:    model.ScanModeShadow,
		// Sampling is OFF by default and independent of the mode: it is a
		// measurement, not enforcement, so it must not ride in on the live switch.
		sampleRate: 0,
		rand:       rand.Float64,
		batchSize:  scanBatchSize,
		interval:   scanInterval,
		now:        time.Now,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// ScanWorkerOption configures an optional worker behaviour.
type ScanWorkerOption func(*ScanWorker)

// WithScanMode sets the enforcement posture from its configuration name
// ("live"; anything else, including a typo or an empty env, is shadow). It takes
// the NAME rather than the constant so the one place that decides what counts as
// "live" is here — a misspelled KUN_TRUST_SCAN_MODE must degrade to the safe
// posture, never to enforcement.
func WithScanMode(name string) ScanWorkerOption {
	return func(w *ScanWorker) {
		if name == "live" {
			w.mode = model.ScanModeLive
			return
		}
		w.mode = model.ScanModeShadow
	}
}

// WithSampleRate sets the share of CLEAN verdicts drawn into the inbox as
// calibration items (0 = off, the default; 0.005 = one in two hundred).
//
// This is the only instrument that can measure the pipeline's FALSE NEGATIVE
// rate. Every other item in the inbox arrives because something already
// suspected it, so the inbox on its own can tell you how often the classifier is
// wrong when it fires — and nothing whatsoever about how often it stays silent
// when it should not. Without a sample the 94% of content scoring below 0.1 is
// simply unexamined, and a model that quietly degrades looks exactly like a
// model that is working.
//
// Out-of-range values clamp to off rather than erroring: a fat-fingered env var
// must not flood a human queue, and must not stop the worker from starting.
func WithSampleRate(rate float64) ScanWorkerOption {
	return func(w *ScanWorker) {
		if rate <= 0 || rate > maxScanSampleRate {
			w.sampleRate = 0
			return
		}
		w.sampleRate = rate
	}
}

// Run drives ScorePending on the worker interval until ctx is cancelled. Errors
// are logged, never fatal — the loop keeps running.
func (w *ScanWorker) Run(ctx context.Context) {
	slog.Info("trust scan worker starting",
		"interval", w.interval.String(), "batch", w.batchSize,
		"gateway_configured", w.gateway.Configured(), "mode", scanModeName(w.mode),
		"sample_rate", w.sampleRate)
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
	// Tier0 is recorded FIRST, on every path (scored + degraded), so even the
	// env-empty drain lands a calibration sample. Pure recording — it never
	// influences the status decision below.
	tier0 := w.tier0Matched(ctx, r)

	// Env empty → drain without a network call (fail-closed: no unbounded backlog).
	if !w.gateway.Configured() {
		return w.markDegraded(tx, r, tier0)
	}
	verdict, err := w.gateway.Moderate(ctx, r.ContentText, r.AuthorID)
	if err != nil {
		slog.Warn("trust scan gateway call failed; draining to degraded", "scan_id", r.ID, "err", err)
		return w.markDegraded(tx, r, tier0)
	}
	if verdict.Degraded {
		// The gateway reported its own fail-open path (upstream down / over budget).
		return w.markDegraded(tx, r, tier0)
	}
	return w.markScored(tx, r, verdict, tier0)
}

// tier0Matched runs the word-list matcher and marshals the result to jsonb: an
// array (possibly []) on success, or nil (leaving the column NULL = "not
// evaluated") when there is no matcher or the match errored. Never blocks the
// row's terminal transition — a matcher failure just yields no Tier0 record.
func (w *ScanWorker) tier0Matched(ctx context.Context, r *model.TrustScanResult) datatypes.JSON {
	if w.tier0 == nil {
		return nil
	}
	matched, err := w.tier0.Tier0Matches(ctx, r.Site, r.ContentText)
	if err != nil {
		slog.Warn("trust tier0 match failed; recording no tier0", "scan_id", r.ID, "err", err)
		return nil
	}
	b, err := json.Marshal(matched) // non-nil [] marshals to "[]", not "null"
	if err != nil {
		slog.Warn("trust tier0 marshal failed; recording no tier0", "scan_id", r.ID, "err", err)
		return nil
	}
	return datatypes.JSON(b)
}

// markScored records a gateway verdict: status=scored plus the verdict fields.
// Uses a map update so a false `flagged` is written explicitly (a scored,
// not-flagged row is flagged=false, distinct from a pending/degraded NULL). A
// flagged verdict logs one info line — a shadow-period observation surface.
func (w *ScanWorker) markScored(tx *gorm.DB, r *model.TrustScanResult, v GatewayVerdict, tier0 datatypes.JSON) error {
	updates := map[string]any{
		"status":    model.ScanStatusScored,
		"mode":      w.mode,
		"flagged":   v.Flagged,
		"channel":   v.Channel,
		"scored_at": w.now(),
	}
	if tier0 != nil {
		updates["tier0_matched"] = tier0
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
		slog.Info("trust scan flagged",
			"scan_id", r.ID, "site", r.Site, "subject_kind", r.SubjectKind, "subject_id", r.SubjectID,
			"channel", v.Channel, "categories", v.Categories, "enforced", w.mode == model.ScanModeLive)
	}
	if err := tx.Model(&model.TrustScanResult{}).Where("id = ?", r.ID).Updates(updates).Error; err != nil {
		return err
	}
	// Live mode only, and only on a conviction: open the inbox item + queue the
	// hide. Sharing this transaction with the verdict is the point — a crash can
	// leave the subject neither scored nor enforced, but never one without the
	// other.
	if v.Flagged {
		if w.mode == model.ScanModeLive {
			return w.enforceFlagged(tx, r, v)
		}
		return nil
	}
	// Cleared. Occasionally send one to a human anyway — the false-negative
	// instrument. Runs in BOTH modes: it enforces nothing, so it does not belong
	// to the live posture.
	return w.maybeSampleClean(tx, r, v)
}

// markDegraded records a drain: status flips to degraded (plus the Tier0 record,
// which is gathered on every path). The verdict fields stay at their pending
// values (flagged/score/categories/scored_at NULL, channel ”), so a degraded row
// reads as "processed, no verdict". Terminal — the worker never re-claims it
// (env-ready powers on for NEW rows, not a re-scan of drained ones, this step).
func (w *ScanWorker) markDegraded(tx *gorm.DB, r *model.TrustScanResult, tier0 datatypes.JSON) error {
	updates := map[string]any{"status": model.ScanStatusDegraded, "mode": w.mode}
	if tier0 != nil {
		updates["tier0_matched"] = tier0
	}
	return tx.Model(&model.TrustScanResult{}).Where("id = ?", r.ID).Updates(updates).Error
}
