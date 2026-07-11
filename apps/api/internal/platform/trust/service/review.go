package service

import (
	"context"
	"errors"
	"time"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReviewService owns the unified review inbox: listing, claiming (FOR UPDATE
// SKIP LOCKED), and the decide state machine (pending/claimed → actioned/
// dismissed) with its disposition + audit side effects.
type ReviewService struct{ db *gorm.DB }

func NewReviewService(db *gorm.DB) *ReviewService { return &ReviewService{db: db} }

// ReviewFilters narrow the queue list. Site "" = all sites (the cross-site
// admin view); Status/Source nil = no filter.
type ReviewFilters struct {
	Site   string
	Status *int16
	Source *int16
	Page   int
	Limit  int
}

// List returns a page of review items, highest priority first (invariant 11
// index), with the total for pagination.
func (s *ReviewService) List(ctx context.Context, f ReviewFilters) ([]model.TrustReviewItem, int64, error) {
	q := s.db.WithContext(ctx).Model(&model.TrustReviewItem{})
	if f.Site != "" {
		q = q.Where("site = ?", f.Site)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.Source != nil {
		q = q.Where("source = ?", *f.Source)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := clampLimit(f.Limit)
	offset := 0
	if f.Page > 1 {
		offset = (f.Page - 1) * limit
	}
	var items []model.TrustReviewItem
	if err := q.Order("priority DESC").Order("id DESC").
		Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Get returns an item plus its associated reports (detail view).
func (s *ReviewService) Get(ctx context.Context, id int64) (*model.TrustReviewItem, []model.TrustReport, error) {
	var item model.TrustReviewItem
	err := s.db.WithContext(ctx).Take(&item, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrReviewItemNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	var reports []model.TrustReport
	if err := s.db.WithContext(ctx).Where("review_item_id = ?", id).
		Order("id ASC").Find(&reports).Error; err != nil {
		return nil, nil, err
	}
	return &item, reports, nil
}

// Claim assigns a pending item to an operator using FOR UPDATE SKIP LOCKED, so
// two concurrent claimers cannot both win — the loser gets 409 (章程 ruling: E6
// probe). A non-pending or absent item is distinguished for 404 vs 409.
func (s *ReviewService) Claim(ctx context.Context, id, claimedBy int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.TrustReviewItem
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("id = ? AND status = ?", id, model.ReviewStatusPending).
			Take(&item).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Either the row is gone (404), or it is not pending / locked by a
			// concurrent claimer (409).
			var exists int64
			if cerr := tx.Model(&model.TrustReviewItem{}).Where("id = ?", id).Count(&exists).Error; cerr != nil {
				return cerr
			}
			if exists == 0 {
				return ErrReviewItemNotFound
			}
			return ErrAlreadyClaimed
		}
		if err != nil {
			return err
		}
		now := time.Now()
		return tx.Model(&model.TrustReviewItem{}).Where("id = ?", id).
			Updates(map[string]any{
				"status":     model.ReviewStatusClaimed,
				"claimed_by": claimedBy,
				"claimed_at": now,
			}).Error
	})
}

// DecideParams carries a decision. Decision is "dismissed" or "actioned";
// Action + ReasonCode are required for "actioned".
type DecideParams struct {
	ID         int64
	DecidedBy  int64
	Decision   string
	Action     *int16
	ReasonCode string
	Statement  *string
}

// Decide runs the terminal state machine. On "actioned" it writes a disposition
// (queued for callback dispatch when the subject_kind has a callback_url); both
// branches append one audit row (章程 ruling 10). An already-terminal item →
// ErrIllegalTransition (409); a malformed actioned → ErrInvalidDecision.
func (s *ReviewService) Decide(ctx context.Context, p DecideParams) (*int64, error) {
	if p.Decision != "dismissed" && p.Decision != "actioned" {
		return nil, ErrInvalidDecision
	}
	if p.Decision == "actioned" && (p.Action == nil || p.ReasonCode == "") {
		return nil, ErrInvalidDecision
	}

	var dispositionID *int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.TrustReviewItem
		lerr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&item, p.ID).Error
		if errors.Is(lerr, gorm.ErrRecordNotFound) {
			return ErrReviewItemNotFound
		}
		if lerr != nil {
			return lerr
		}
		if item.Status != model.ReviewStatusPending && item.Status != model.ReviewStatusClaimed {
			return ErrIllegalTransition
		}

		now := time.Now()
		if p.Decision == "dismissed" {
			if err := tx.Model(&model.TrustReviewItem{}).Where("id = ?", p.ID).
				Updates(map[string]any{
					"status":     model.ReviewStatusDismissed,
					"decided_by": p.DecidedBy,
					"decided_at": now,
				}).Error; err != nil {
				return err
			}
			return AppendAudit(tx, AuditEntry{
				ActorID: &p.DecidedBy, Action: "review_dismissed",
				Site: strptr(item.Site), SubjectKind: strptr(item.SubjectKind), SubjectID: strptr(item.SubjectID),
			})
		}

		// actioned
		if err := tx.Model(&model.TrustReviewItem{}).Where("id = ?", p.ID).
			Updates(map[string]any{
				"status":     model.ReviewStatusActioned,
				"decided_by": p.DecidedBy,
				"decided_at": now,
			}).Error; err != nil {
			return err
		}

		// Does the subject_kind have a callback endpoint?
		var kind model.TrustSubjectKind
		kerr := tx.Where("site = ? AND key = ?", item.Site, item.SubjectKind).Take(&kind).Error
		if kerr != nil && !errors.Is(kerr, gorm.ErrRecordNotFound) {
			return kerr
		}
		disp := model.TrustDisposition{
			ReviewItemID: item.ID, Action: *p.Action, ActedBy: p.DecidedBy,
			ReasonCode: p.ReasonCode, Statement: p.Statement,
		}
		if kind.CallbackURL != nil && *kind.CallbackURL != "" {
			pending := model.CallbackStatusPending
			disp.CallbackStatus = &pending
			disp.NextAttemptAt = &now
		}
		if err := tx.Create(&disp).Error; err != nil {
			return err
		}
		dispositionID = &disp.ID
		return AppendAudit(tx, AuditEntry{
			ActorID: &p.DecidedBy, Action: "review_actioned",
			Site: strptr(item.Site), SubjectKind: strptr(item.SubjectKind), SubjectID: strptr(item.SubjectID),
			ReasonCode: strptr(p.ReasonCode),
		})
	})
	if err != nil {
		return nil, err
	}
	return dispositionID, nil
}

// clampLimit bounds a page size (shared default with the community convention).
func clampLimit(limit int) int {
	const def = 50
	if limit <= 0 || limit > 200 {
		return def
	}
	return limit
}
