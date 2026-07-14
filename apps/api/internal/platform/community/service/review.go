package service

import (
	"context"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"

	"gorm.io/gorm"
)

// ReviewService owns the centralized moderation queue (Coral single queue +
// per-site filter). Adjudication is about the CONTENT: approve keeps it, reject
// removes it. A decision on a flags item backfills every reporter's accuracy,
// which feeds their future report weight (the reputation loop).
type ReviewService struct {
	db      *gorm.DB
	reviews *repository.ReviewRepository
	sink    EventSink
}

func NewReviewService(db *gorm.DB, sink EventSink) *ReviewService {
	return &ReviewService{db: db, reviews: repository.NewReviewRepository(db), sink: sink}
}

// List returns a site's pending queue items, each joined to its subject post's
// thread + author (the consuming site's deep-link + author resolve). source ≥ 0
// filters to one source (ReviewSource* constants); source < 0 = all sources.
func (s *ReviewService) List(site string, source int16, limit int) ([]repository.ReviewItemRow, error) {
	return s.reviews.ListPending(site, source, clampLimit(limit))
}

// Approve keeps the content: the post is restored to visible. For a flags item
// the reports were wrong → pending flags become disagreed and each reporter's
// accuracy is backfilled. A first_post_hold release does NOT refund the hold
// counter (one-way consumption).
func (s *ReviewService) Approve(ctx context.Context, id, decidedBy int64) error {
	return s.decide(ctx, id, decidedBy, true)
}

// Reject removes the content: the post is tombstoned. For a flags item the
// reports were right → pending flags become agreed and accuracy is backfilled.
func (s *ReviewService) Reject(ctx context.Context, id, decidedBy int64) error {
	return s.decide(ctx, id, decidedBy, false)
}

func (s *ReviewService) decide(ctx context.Context, id, decidedBy int64, approve bool) error {
	var forwarded bool // the item was forwarded to trust → resolve it after commit
	txErr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		item, err := repository.GetReviewItemTx(tx, id)
		if err != nil {
			return err
		}
		if item == nil {
			return ErrReviewNotFound
		}
		// Tenant guard on the already-loaded item (ruling 4): a review item carries
		// its own site column and is always site-local, so a caller may only decide
		// its OWN site's queue. A cross-tenant (or site-less) item is answered as
		// "review item not found" — indistinguishable from a bad id. An internal
		// caller ("") skips the guard.
		if cs := callerSite(ctx); cs != "" && (item.Site == nil || *item.Site != cs) {
			return ErrReviewNotFound
		}
		if item.Status != model.ReviewStatusPending {
			return nil // already decided → idempotent no-op
		}
		if item.PostID != nil {
			postID := *item.PostID
			newStatus := model.PostStatusDeleted
			if approve {
				newStatus = model.PostStatusVisible
			}
			if err := repository.SetPostStatusTx(tx, postID, newStatus); err != nil {
				return err
			}
			if item.Source != nil && *item.Source == model.ReviewSourceFlags {
				if err := s.backfillFlags(tx, postID, approve); err != nil {
					return err
				}
			}
		}
		forwarded = item.TrustReviewItemID != nil
		status := model.ReviewStatusRejected
		if approve {
			status = model.ReviewStatusApproved
		}
		return repository.DecideReviewItemTx(tx, id, status, decidedBy)
	})
	if txErr != nil {
		return txErr
	}
	// Two-master convergence (step 03 ruling 1): a local decision on a forwarded
	// item best-effort resolves the trust item — WITHOUT a callback (no echo
	// loop). If a platform operator already decided it on the trust side, the
	// resolve lands on a terminal item and returns closed:false (a tolerated
	// race). The sink turns this into an off-request trust call; a no-op
	// otherwise.
	if forwarded && s.sink != nil {
		kind := EventReviewRejected
		if approve {
			kind = EventReviewApproved
		}
		s.sink.Emit(Event{Kind: kind, ReviewItemID: id, ActorID: decidedBy})
	}
	return nil
}

// backfillFlags resolves a post's pending flags and updates each reporter's
// accuracy: approve → the reports were wrong (disagreed); reject → the reports
// were right (agreed).
func (s *ReviewService) backfillFlags(tx *gorm.DB, postID int64, approve bool) error {
	flaggers, err := repository.PendingFlaggersTx(tx, postID)
	if err != nil {
		return err
	}
	reportsWereRight := !approve
	for _, f := range flaggers {
		if err := repository.IncFlagAccuracyTx(tx, f, reportsWereRight); err != nil {
			return err
		}
	}
	flagStatus := model.FlagStatusDisagreed
	if reportsWereRight {
		flagStatus = model.FlagStatusAgreed
	}
	return repository.ResolvePendingFlagsTx(tx, postID, flagStatus)
}
