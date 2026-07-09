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
}

func NewReviewService(db *gorm.DB) *ReviewService {
	return &ReviewService{db: db, reviews: repository.NewReviewRepository(db)}
}

// List returns a site's pending queue items. source ≥ 0 filters to one source
// (ReviewSource* constants); source < 0 = all sources.
func (s *ReviewService) List(site string, source int16, limit int) ([]model.CommunityReviewItem, error) {
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
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		item, err := repository.GetReviewItemTx(tx, id)
		if err != nil {
			return err
		}
		if item == nil {
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
		status := model.ReviewStatusRejected
		if approve {
			status = model.ReviewStatusApproved
		}
		return repository.DecideReviewItemTx(tx, id, status, decidedBy)
	})
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
