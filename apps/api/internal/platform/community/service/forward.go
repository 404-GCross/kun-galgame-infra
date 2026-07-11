package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"
	"api/pkg/trustclient"

	"gorm.io/gorm"
)

// Step 03 trust-forwarding constants.
const (
	// forwardSubjectKind is the trust subject_kind every community-backed site
	// registers (one row per site, callback_url → this service's /trust/callback).
	forwardSubjectKind = "community_post"
	// forwardBatchSize bounds one outbox sweep (low-volume v0).
	forwardBatchSize = 50
	// forwardErrorThreshold escalates the retry log to error level once a row has
	// failed this many sweeps (no dead-letter — the row stays retryable forever).
	forwardErrorThreshold = 5
	// contextNoteMaxRunes caps the content excerpt in the forwarded context note.
	contextNoteMaxRunes = 500

	outcomeApproved = "approved"
	outcomeRejected = "rejected"
)

// Forwarder is the trust forward-face client, abstracted so tests fake it.
// *trustclient.Client satisfies it directly.
type Forwarder interface {
	Forward(ctx context.Context, req trustclient.ForwardRequest) (trustItemID int64, created bool, err error)
	Resolve(ctx context.Context, trustItemID int64, outcome, actorRef string) (closed bool, err error)
}

// ForwardService is the community-side half of the two-master convergence
// (step 03): it forwards local review items into the unified trust inbox
// (outbox pattern) and resolves them back after a local moderator decision.
// A nil Forwarder = forwarding disabled (fail-closed): every path is a no-op and
// the outbox ticker idles.
type ForwardService struct {
	db *gorm.DB
	fw Forwarder
}

// NewForwardService builds the service. fw nil = forwarding off.
func NewForwardService(db *gorm.DB, fw Forwarder) *ForwardService {
	return &ForwardService{db: db, fw: fw}
}

// Enabled reports whether forwarding is wired.
func (s *ForwardService) Enabled() bool { return s.fw != nil }

// ForwardItemBg is the immediate best-effort forward (goroutine-safe, logs on
// failure — the ticker is the reliable path).
func (s *ForwardService) ForwardItemBg(itemID int64) {
	if err := s.forwardItem(context.Background(), itemID); err != nil {
		slog.Warn("community trust-forward (immediate)", "item_id", itemID, "err", err)
	}
}

// forwardItem forwards a single review item and back-fills trust_review_item_id
// on success. Idempotent: a row already forwarded (or gone) is a no-op.
func (s *ForwardService) forwardItem(ctx context.Context, itemID int64) error {
	if s.fw == nil {
		return nil
	}
	ft, found, err := repository.LoadForwardTargetTx(s.db.WithContext(ctx), itemID)
	if err != nil {
		return err
	}
	if !found || ft.Forwarded {
		return nil
	}
	trustID, _, err := s.fw.Forward(ctx, s.buildRequest(ft))
	if err != nil {
		return err
	}
	return repository.SetTrustReviewItemIDTx(s.db.WithContext(ctx), itemID, trustID)
}

// Sweep is the outbox ticker body: it claims a batch of un-forwarded rows (FOR
// UPDATE SKIP LOCKED), increments each row's attempt counter, forwards it, and
// back-fills the trust id on success. A forward failure is logged (escalating to
// error past the threshold) and left for the next sweep — there is no dead-letter.
// Returns the number of rows forwarded. HTTP runs inside the sweep transaction
// (mirroring the trust callback worker's low-volume v0 shape); it is NOT the
// creating transaction, so ruling 2's "no HTTP inside the creating tx" holds.
func (s *ForwardService) Sweep(ctx context.Context) (int, error) {
	if s.fw == nil {
		return 0, nil
	}
	forwarded := 0
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := repository.LockUnforwardedTx(tx, forwardBatchSize)
		if err != nil {
			return err
		}
		for i := range rows {
			row := &rows[i]
			if err := repository.BumpForwardAttemptsTx(tx, row.ID); err != nil {
				return err
			}
			attempts := row.ForwardAttempts + 1
			author, content, ok, err := repository.PostBodyTx(tx, *row.PostID)
			if err != nil {
				return err
			}
			if !ok {
				continue // post gone (community never hard-deletes) — skip
			}
			ft := &repository.ForwardTarget{
				ItemID: row.ID, Site: *row.Site, PostID: *row.PostID, Source: row.Source,
				AuthorID: author, ContentRaw: content,
			}
			trustID, _, ferr := s.fw.Forward(ctx, s.buildRequest(ft))
			if ferr != nil {
				if int(attempts) >= forwardErrorThreshold {
					slog.Error("community trust-forward sweep", "item_id", row.ID, "attempts", attempts, "err", ferr)
				} else {
					slog.Warn("community trust-forward sweep", "item_id", row.ID, "attempts", attempts, "err", ferr)
				}
				continue // leave unforwarded — retried next sweep
			}
			if err := repository.SetTrustReviewItemIDTx(tx, row.ID, trustID); err != nil {
				return err
			}
			forwarded++
		}
		return nil
	})
	return forwarded, err
}

// ResolveItemBg best-effort resolves a forwarded item after a local decision
// (goroutine-safe, logs on failure).
func (s *ForwardService) ResolveItemBg(itemID int64, outcome string, actorID int64) {
	if err := s.resolveItem(context.Background(), itemID, outcome, actorID); err != nil {
		slog.Warn("community trust-resolve", "item_id", itemID, "outcome", outcome, "err", err)
	}
}

// resolveItem closes the forwarded trust item for a locally-decided review item.
// A row that was never forwarded (no trust id) is a no-op.
func (s *ForwardService) resolveItem(ctx context.Context, itemID int64, outcome string, actorID int64) error {
	if s.fw == nil {
		return nil
	}
	row, err := repository.GetReviewItemTx(s.db.WithContext(ctx), itemID)
	if err != nil {
		return err
	}
	if row == nil || row.TrustReviewItemID == nil {
		return nil
	}
	_, err = s.fw.Resolve(ctx, *row.TrustReviewItemID, outcome, strconv.FormatInt(actorID, 10))
	return err
}

// buildRequest assembles the forward payload from a target row.
func (s *ForwardService) buildRequest(ft *repository.ForwardTarget) trustclient.ForwardRequest {
	note := forwardContextNote(ft)
	ref := strconv.FormatInt(ft.ItemID, 10)
	return trustclient.ForwardRequest{
		Site: ft.Site, SubjectKind: forwardSubjectKind, SubjectID: strconv.FormatInt(ft.PostID, 10),
		ContextNote: &note, ForwarderRef: &ref,
	}
}

// forwardContextNote formats the reviewer-facing evidence excerpt (ruling 5):
// "[source] post #<id> by user <id>: <content excerpt ≤500>".
func forwardContextNote(ft *repository.ForwardTarget) string {
	return fmt.Sprintf("[%s] post #%d by user %d: %s",
		forwardSourceLabel(ft.Source), ft.PostID, ft.AuthorID, truncateRunes(ft.ContentRaw, contextNoteMaxRunes))
}

// forwardSourceLabel maps a ReviewSource* constant to a human label.
func forwardSourceLabel(source *int16) string {
	if source == nil {
		return "unknown"
	}
	switch *source {
	case model.ReviewSourceFlags:
		return "flags"
	case model.ReviewSourceFirstPostHold:
		return "first_post_hold"
	case model.ReviewSourceSuspectWords:
		return "suspect_words"
	case model.ReviewSourceExternal:
		return "external"
	default:
		return "unknown"
	}
}

// truncateRunes trims a string to at most n runes (never splitting a rune).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ForwardingSink decorates an inner EventSink, turning the step-03 review.*
// events into off-request trust forward/resolve calls. It leaves every other
// event to the inner sink (notification concerns stay separate). Composed in
// cmd/community only when forwarding is wired; the services stay unaware.
type ForwardingSink struct {
	inner EventSink
	fwd   *ForwardService
}

// NewForwardingSink composes a forwarding decorator over an inner sink.
func NewForwardingSink(inner EventSink, fwd *ForwardService) ForwardingSink {
	return ForwardingSink{inner: inner, fwd: fwd}
}

// Emit implements EventSink. Trust calls run off-request (goroutine) so a slow /
// down trust service never blocks the create path; the outbox ticker is the
// reliable backstop.
func (s ForwardingSink) Emit(e Event) {
	if s.inner != nil {
		s.inner.Emit(e)
	}
	if s.fwd == nil || !s.fwd.Enabled() {
		return
	}
	switch e.Kind {
	case EventReviewEnqueued:
		go s.fwd.ForwardItemBg(e.ReviewItemID)
	case EventReviewApproved:
		go s.fwd.ResolveItemBg(e.ReviewItemID, outcomeApproved, e.ActorID)
	case EventReviewRejected:
		go s.fwd.ResolveItemBg(e.ReviewItemID, outcomeRejected, e.ActorID)
	}
}
