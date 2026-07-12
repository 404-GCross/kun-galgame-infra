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

// Get returns an item plus its associated reports (detail view). siteScope, when
// non-empty, confines the lookup to that site: a foreign-site item reads as
// ErrReviewItemNotFound, so a site-scoped caller cannot even learn it exists.
func (s *ReviewService) Get(ctx context.Context, id int64, siteScope string) (*model.TrustReviewItem, []model.TrustReport, error) {
	var item model.TrustReviewItem
	q := s.db.WithContext(ctx)
	if siteScope != "" {
		q = q.Where("site = ?", siteScope)
	}
	err := q.Take(&item, id).Error
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
// probe). A non-pending or absent item is distinguished for 404 vs 409. siteScope,
// when non-empty, confines the claim to that site: a foreign-site item reads as
// ErrReviewItemNotFound (404), never ErrAlreadyClaimed, so existence never leaks.
func (s *ReviewService) Claim(ctx context.Context, id, claimedBy int64, siteScope string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.TrustReviewItem
		lockQ := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("id = ? AND status = ?", id, model.ReviewStatusPending)
		if siteScope != "" {
			lockQ = lockQ.Where("site = ?", siteScope)
		}
		err := lockQ.Take(&item).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Either the row is gone / out of scope (404), or it is not pending /
			// locked by a concurrent claimer (409). The existence probe is scoped
			// too, so a foreign-site item counts as absent (404), not a conflict.
			existsQ := tx.Model(&model.TrustReviewItem{}).Where("id = ?", id)
			if siteScope != "" {
				existsQ = existsQ.Where("site = ?", siteScope)
			}
			var exists int64
			if cerr := existsQ.Count(&exists).Error; cerr != nil {
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
// Action + ReasonCode are required for "actioned". SiteScope, when non-empty,
// confines the decision to that site: a foreign-site item reads as
// ErrReviewItemNotFound (404), so a site-scoped caller cannot decide it.
type DecideParams struct {
	ID         int64
	DecidedBy  int64
	Decision   string
	Action     *int16
	ReasonCode string
	Statement  *string
	SiteScope  string
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
		lockQ := tx.Clauses(clause.Locking{Strength: "UPDATE"})
		if p.SiteScope != "" {
			lockQ = lockQ.Where("site = ?", p.SiteScope)
		}
		lerr := lockQ.Take(&item, p.ID).Error
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
			// notify_on_dismiss (ruling 4): a kind that asks for it AND has a
			// callback endpoint gets an action:0 (none) disposition + pending
			// callback, so the product releases a hold / un-hides on "reviewed,
			// no problem". Ordinary report kinds leave notify_on_dismiss false →
			// no disposition, exactly as before.
			var kind model.TrustSubjectKind
			kerr := tx.Where("site = ? AND key = ?", item.Site, item.SubjectKind).Take(&kind).Error
			if kerr != nil && !errors.Is(kerr, gorm.ErrRecordNotFound) {
				return kerr
			}
			if kind.NotifyOnDismiss && kind.CallbackURL != nil && *kind.CallbackURL != "" {
				pending := model.CallbackStatusPending
				disp := model.TrustDisposition{
					ReviewItemID: item.ID, Action: model.ActionNone, ActedBy: p.DecidedBy,
					ReasonCode: "review_dismissed", CallbackStatus: &pending, NextAttemptAt: &now,
				}
				if err := tx.Create(&disp).Error; err != nil {
					return err
				}
				dispositionID = &disp.ID
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
