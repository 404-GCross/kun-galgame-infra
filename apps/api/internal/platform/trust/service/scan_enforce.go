package service

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// scanModeName renders a mode for logs — the startup line is how an operator
// confirms which posture a deployment actually came up in.
func scanModeName(mode int16) string {
	if mode == model.ScanModeLive {
		return "live"
	}
	return "shadow"
}

// scanActorSystem is the acted_by written on an AI-driven disposition. The
// column is NOT NULL (it models an operator id) and no human acted here, so 0 is
// the system sentinel — the audit row is what records that this was machine
// enforcement, via action="scan_auto_actioned".
const scanActorSystem int64 = 0

// scanReasonCode is the machine reason on every AI-driven hide.
const scanReasonCode = "ai_scan_flagged"

// scanNoteExcerptRunes bounds the reviewer-facing excerpt in context_note. The
// note exists so a human can judge without opening the product, not to duplicate
// the content — and trust_review_item is served over an API surface whereas
// content_text deliberately is not (json:"-").
const scanNoteExcerptRunes = 200

// enforceFlagged is the live-mode side effect of a flagged verdict (doc 18 §6
// P2). It opens — idempotently — one review item for the subject and queues one
// hide disposition, which the existing callback worker delivers to the product
// (community then hides the post and the human works it from the trust inbox).
//
// Idempotency rests on invariant 4: at most ONE open item per subject. A repeat
// scan of the same subject (every edit is a fresh scan row) finds the open item
// and only RAISES its classifier_score — it never forks a second item and never
// queues a second hide. That also means a subject already open from a user
// report absorbs the AI signal instead of being actioned behind the reporter's
// back: the disposition is queued only when THIS call created the item.
//
// A disposition gets callback_status=pending only when the subject_kind carries
// a callback_url; without one the item is human-only enforcement and simply
// waits in the inbox (the same rule Decide follows).
func (w *ScanWorker) enforceFlagged(tx *gorm.DB, r *model.TrustScanResult, v GatewayVerdict) error {
	itemID, created, err := w.openScanReviewItem(tx, r, v)
	if err != nil {
		return err
	}
	if !created {
		return nil // an open item already carries this subject — signal merged, no second hide
	}

	var kind model.TrustSubjectKind
	kerr := tx.Where("site = ? AND key = ?", r.Site, r.SubjectKind).Take(&kind).Error
	if kerr != nil && !errors.Is(kerr, gorm.ErrRecordNotFound) {
		return kerr
	}
	now := w.now()
	disp := model.TrustDisposition{
		ReviewItemID: itemID, Action: model.ActionHide, ActedBy: scanActorSystem,
		ReasonCode: scanReasonCode,
	}
	if kind.CallbackURL != nil && *kind.CallbackURL != "" {
		pending := model.CallbackStatusPending
		disp.CallbackStatus = &pending
		disp.NextAttemptAt = &now
	}
	if err := tx.Create(&disp).Error; err != nil {
		return err
	}
	return AppendAudit(tx, AuditEntry{
		// ActorID stays NULL: no operator acted (章程 — system-driven rows).
		Action: "scan_auto_actioned",
		Site:   strptr(r.Site), SubjectKind: strptr(r.SubjectKind), SubjectID: strptr(r.SubjectID),
		ReasonCode: strptr(scanReasonCode),
	})
}

// openScanReviewItem returns the open review item for the scan's subject,
// creating one with source=ai_text when none exists. It mirrors the forward
// service's shape (ruling 1: update the open item, never fork), including the
// OnConflict backstop for a concurrent opener.
func (w *ScanWorker) openScanReviewItem(tx *gorm.DB, r *model.TrustScanResult, v GatewayVerdict) (int64, bool, error) {
	note := scanContextNote(r, v)
	openStates := []int16{model.ReviewStatusPending, model.ReviewStatusClaimed}

	var open model.TrustReviewItem
	lerr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("site = ? AND subject_kind = ? AND subject_id = ? AND status IN ?",
			r.Site, r.SubjectKind, r.SubjectID, openStates).
		Limit(1).Take(&open).Error
	if lerr == nil {
		updates := map[string]any{}
		if v.Score != nil {
			updates["classifier_score"] = gorm.Expr("GREATEST(COALESCE(classifier_score, 0), ?)", *v.Score)
		}
		// Fill only when empty — a later scan never clobbers the first evidence.
		updates["context_note"] = gorm.Expr(
			"CASE WHEN context_note IS NULL OR context_note = '' THEN ? ELSE context_note END", note)
		// Every edit re-scans, so this is where a subject that has kept accruing
		// views since the item opened gets re-ranked.
		if reach := maxReach(open.SubjectReach, r.SubjectReach); reach != nil {
			updates["subject_reach"] = *reach
			updates["priority"] = repriceForReach(open.Priority, open.SubjectReach, reach)
		}
		if err := tx.Model(&model.TrustReviewItem{}).Where("id = ?", open.ID).Updates(updates).Error; err != nil {
			return 0, false, err
		}
		return open.ID, false, nil
	} else if !errors.Is(lerr, gorm.ErrRecordNotFound) {
		return 0, false, lerr
	}

	item := model.TrustReviewItem{
		Site: r.Site, SubjectKind: r.SubjectKind, SubjectID: r.SubjectID,
		Source: model.ReviewSourceAIText, ClassifierScore: v.Score,
		ContextNote: &note, SubjectReach: r.SubjectReach,
		Priority: rankPriority(scanPriority(v.Score), r.SubjectReach),
		Status:   model.ReviewStatusPending,
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
	if res.Error != nil {
		return 0, false, res.Error
	}
	if res.RowsAffected == 0 {
		// A concurrent report/forward opened it first (invariant 4 backstop) →
		// adopt it rather than forking, and leave enforcement to whoever owns it.
		if err := tx.Where("site = ? AND subject_kind = ? AND subject_id = ? AND status IN ?",
			r.Site, r.SubjectKind, r.SubjectID, openStates).
			Limit(1).Take(&item).Error; err != nil {
			return 0, false, err
		}
		return item.ID, false, nil
	}
	return item.ID, true, nil
}

// maybeSampleClean draws a CLEARED scan into the inbox with probability
// sampleRate, so a human periodically checks what the classifier waved through.
//
// The three properties that make this safe to leave on permanently:
//
//   - It never enforces. No disposition, no callback, nothing is hidden. The
//     item exists to be looked at and dismissed.
//   - It never competes with real work. A sample lands at scanSamplePriority,
//     below every genuine signal, so it is only ever reached by a reviewer who
//     has already cleared the queue.
//   - It never disturbs an in-flight case. If the subject already has an open
//     item, the sample is skipped entirely — a subject under review is not a
//     random draw, and folding a calibration item into a real one would corrupt
//     both the case and the measurement.
func (w *ScanWorker) maybeSampleClean(tx *gorm.DB, r *model.TrustScanResult, v GatewayVerdict) error {
	if w.sampleRate <= 0 || w.rand() >= w.sampleRate {
		return nil
	}

	var open int64
	if err := tx.Model(&model.TrustReviewItem{}).
		Where("site = ? AND subject_kind = ? AND subject_id = ? AND status IN ?",
			r.Site, r.SubjectKind, r.SubjectID,
			[]int16{model.ReviewStatusPending, model.ReviewStatusClaimed}).
		Count(&open).Error; err != nil {
		return err
	}
	if open > 0 {
		return nil
	}

	note := scanSampleNote(r, v)
	item := model.TrustReviewItem{
		Site: r.Site, SubjectKind: r.SubjectKind, SubjectID: r.SubjectID,
		Source: model.ReviewSourceAISample, ClassifierScore: v.Score,
		ContextNote: &note, SubjectReach: r.SubjectReach,
		// Deliberately NOT reach-boosted: a sample must stay at the bottom of the
		// queue whatever its subject's reach, or a popular post would turn a
		// calibration draw into an urgent-looking item.
		Priority: scanSamplePriority, Status: model.ReviewStatusPending,
	}
	// DoNothing covers the race with a concurrent opener; a lost sample is a lost
	// data point, never an error worth failing the scan transaction over.
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item).Error; err != nil {
		return err
	}
	slog.Info("trust scan sampled clean verdict",
		"scan_id", r.ID, "site", r.Site, "subject_kind", r.SubjectKind, "subject_id", r.SubjectID)
	return AppendAudit(tx, AuditEntry{
		Action: "scan_sampled_clean",
		Site:   strptr(r.Site), SubjectKind: strptr(r.SubjectKind), SubjectID: strptr(r.SubjectID),
	})
}

// scanSampleNote tells the reviewer what they are looking at and what the
// answer means — a sample is a QUESTION ("did we miss anything here?"), not an
// accusation, and a reviewer who reads it as one will dismiss the whole batch
// without looking and the measurement will silently read zero.
func scanSampleNote(r *model.TrustScanResult, v GatewayVerdict) string {
	var b strings.Builder
	b.WriteString("[calibration] the classifier cleared this")
	if v.Score != nil {
		fmt.Fprintf(&b, " (score=%.3f)", *v.Score)
	}
	b.WriteString(" — it was drawn at random to check that verdict. ")
	b.WriteString("Dismiss if it is genuinely fine; action it if we missed something.\n")
	b.WriteString(excerptRunes(r.ContentText, scanNoteExcerptRunes))
	return b.String()
}

// scanPriority ranks an AI-opened item by the classifier's confidence. The
// inbox's priority is "reach × severity ÷ cost"; for a machine verdict the score
// is the only severity signal available, scaled to the 0-5 band the report-driven
// sources use so the two sort comparably.
func scanPriority(score *float32) float32 {
	if score == nil {
		return 1
	}
	return *score * 5
}

// scanContextNote builds the reviewer-facing evidence line: the convicting
// categories plus a bounded excerpt of the scanned text.
func scanContextNote(r *model.TrustScanResult, v GatewayVerdict) string {
	var b strings.Builder
	b.WriteString("[ai]")
	if len(v.Categories) > 0 {
		fmt.Fprintf(&b, " %s", strings.Join(v.Categories, ", "))
	}
	if v.Score != nil {
		fmt.Fprintf(&b, " score=%.3f", *v.Score)
	}
	if r.AuthorID != nil {
		fmt.Fprintf(&b, " by user %d", *r.AuthorID)
	}
	b.WriteString(": ")
	b.WriteString(excerptRunes(r.ContentText, scanNoteExcerptRunes))
	return b.String()
}

// excerptRunes truncates to at most n runes, appending an ellipsis when it cut.
// Rune-based so multibyte CJK text is never split mid-glyph.
func excerptRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i] + "…"
		}
		count++
	}
	return s
}
