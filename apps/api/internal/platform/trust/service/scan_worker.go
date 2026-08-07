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
// PER-SITE POSTURE (step 07 M0): mode and sampleRate below are the PLATFORM
// DEFAULTS, not the posture in force. Every row is governed by policy.Resolve(
// row.Site), which returns those defaults unless the site has said otherwise —
// so a site with no policy row behaves exactly as it did before the table
// existed. Resolution happens per row rather than per worker because one worker
// drains every tenant's queue: the posture belongs to the content, not to the
// process that happens to pick it up.
type ScanWorker struct {
	db      *gorm.DB
	gateway ScanGateway
	tier0   Tier0Matcher
	// policy resolves the per-site posture; nil = no table lookup, the platform
	// defaults govern every site (the shape the unit tests use).
	policy     *PolicyService
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
	return func(w *ScanWorker) { w.mode = ScanModeFromName(name) }
}

// ScanModeFromName is the ONE place that decides what counts as "live". A
// misspelled KUN_TRUST_SCAN_MODE, or an empty one, must degrade to the safe
// posture — never to enforcement — so anything that is not exactly "live" is
// shadow.
func ScanModeFromName(name string) int16 {
	if name == "live" {
		return model.ScanModeLive
	}
	return model.ScanModeShadow
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

// WithPolicy gives the worker the per-site policy resolver. main passes the same
// instance the admin face writes through, so an operator's change reaches the
// worker as soon as the write invalidates the cache rather than a TTL later.
func WithPolicy(p *PolicyService) ScanWorkerOption {
	return func(w *ScanWorker) { w.policy = p }
}

// policyFor is the single place a row's governing posture is decided. Without a
// resolver (unit tests) it reports the worker's own defaults, so the two paths
// cannot drift in what "no override" means.
func (w *ScanWorker) policyFor(site string) ResolvedPolicy {
	if w.policy == nil {
		return ResolvedPolicy{
			ScanMode:        w.mode,
			SampleRate:      w.sampleRate,
			AutoHideEnabled: true,
		}
	}
	return w.policy.Resolve(site)
}

// Run drives ScorePending on the worker interval until ctx is cancelled. Errors
// are logged, never fatal — the loop keeps running.
func (w *ScanWorker) Run(ctx context.Context) {
	// The posture is logged as the PLATFORM DEFAULT, since that is all the worker
	// knows at startup — a site with an override is governed by the table, and
	// the console is where that is visible. Naming it "default" in the line keeps
	// an operator from reading this as the posture in force everywhere.
	slog.Info("trust scan worker starting",
		"interval", w.interval.String(), "batch", w.batchSize,
		"gateway_configured", w.gateway.Configured(),
		"default_mode", scanModeName(w.mode), "default_sample_rate", w.sampleRate,
		"per_site_policy", w.policy != nil)
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
		// Fresh intake first, then degraded rows that have attempts left. The
		// ordering is load-bearing: a persistently failing upstream produces a
		// growing pool of retryable rows, and without `status ASC` those old rows
		// would fill every batch and starve the content being published right now.
		// Retrying yesterday's failure is never more urgent than judging today's post.
		var pending []model.TrustScanResult
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? OR (status = ? AND scan_attempts < ?)",
				model.ScanStatusPending, model.ScanStatusDegraded, maxScanAttempts).
			Order("status ASC, id ASC").Limit(w.batchSize).Find(&pending).Error; err != nil {
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
	pol := w.policyFor(r.Site)

	// Env empty → drain without a network call (fail-closed: no unbounded backlog).
	if !w.gateway.Configured() {
		return w.markDegraded(tx, r, tier0, pol, model.ScanDegradedGatewayUnconfigured)
	}
	verdict, err := w.gateway.Moderate(ctx, r.ContentText, r.AuthorID)
	if err != nil {
		slog.Warn("trust scan gateway call failed; draining to degraded",
			"scan_id", r.ID, "attempt", r.ScanAttempts+1, "err", err)
		return w.markDegraded(tx, r, tier0, pol, model.ScanDegradedGatewayCallFailed)
	}
	if verdict.Degraded {
		// The gateway reported its own fail-open path (upstream down / over budget /
		// a reply it could not parse). This branch logged NOTHING until 2026-08-07,
		// which is precisely why it drained 262 rows unnoticed — it is the most
		// common drain, and it was the only one that was silent. The gateway's own
		// ai_usage row holds the specific cause; this line is what makes anyone go
		// look for it.
		slog.Warn("trust scan gateway returned a degraded verdict; draining to degraded",
			"scan_id", r.ID, "site", r.Site, "attempt", r.ScanAttempts+1,
			"channel", verdict.Channel)
		return w.markDegraded(tx, r, tier0, pol, model.ScanDegradedGatewayDegraded)
	}
	return w.markScored(tx, r, verdict, tier0, pol)
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
func (w *ScanWorker) markScored(tx *gorm.DB, r *model.TrustScanResult, v GatewayVerdict, tier0 datatypes.JSON, pol ResolvedPolicy) error {
	updates := map[string]any{
		"status": model.ScanStatusScored,
		"mode":   pol.ScanMode,
		// flagged and gateway_flagged are written from the SAME value today. They
		// are separate columns because M0-B lets a site re-derive flagged from
		// score against its own threshold, and gateway_flagged is what keeps rows
		// from before and after that change comparable.
		"flagged":         v.Flagged,
		"gateway_flagged": v.Flagged,
		"channel":         v.Channel,
		"scored_at":       w.now(),
		"scan_attempts":   r.ScanAttempts + 1,
		// Cleared on purpose: this row may be a retry of an earlier drain, and a
		// scored row carrying a degradation reason would read as both at once.
		// The attempt count is what preserves the fact that it took more than one try.
		"degraded_reason": nil,
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
			"channel", v.Channel, "categories", v.Categories, "enforced", pol.ScanMode == model.ScanModeLive)
	}
	if err := tx.Model(&model.TrustScanResult{}).Where("id = ?", r.ID).Updates(updates).Error; err != nil {
		return err
	}
	// Live mode only, and only on a conviction: open the inbox item + queue the
	// hide. Sharing this transaction with the verdict is the point — a crash can
	// leave the subject neither scored nor enforced, but never one without the
	// other.
	if v.Flagged {
		if pol.ScanMode == model.ScanModeLive {
			return w.enforceFlagged(tx, r, v, pol)
		}
		return nil
	}
	// Cleared. Occasionally send one to a human anyway — the false-negative
	// instrument. Runs in BOTH modes: it enforces nothing, so it does not belong
	// to the live posture.
	return w.maybeSampleClean(tx, r, v, pol)
}

// markDegraded records a drain: status flips to degraded (plus the Tier0 record,
// which is gathered on every path) along with WHY and the attempt count. The
// verdict fields stay at their pending values (flagged/score/categories/scored_at
// NULL, channel ”), so a degraded row reads as "processed, no verdict".
//
// No longer terminal: the worker re-claims a degraded row until scan_attempts
// reaches maxScanAttempts. A row that exhausts its attempts stays degraded for
// good and is what the operator surface should count — that is a real unjudged
// item, as opposed to one merely waiting for the next tick.
func (w *ScanWorker) markDegraded(tx *gorm.DB, r *model.TrustScanResult, tier0 datatypes.JSON, pol ResolvedPolicy, reason int16) error {
	updates := map[string]any{
		"status":          model.ScanStatusDegraded,
		"mode":            pol.ScanMode,
		"degraded_reason": reason,
		"scan_attempts":   r.ScanAttempts + 1,
	}
	if tier0 != nil {
		updates["tier0_matched"] = tier0
	}
	return tx.Model(&model.TrustScanResult{}).Where("id = ?", r.ID).Updates(updates).Error
}
