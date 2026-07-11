package service

import (
	"context"
	"errors"
	"time"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ForwardService owns the community→trust convergence face (step 03): a product
// (community) that runs its own local moderation queue forwards its over-threshold
// / held items into the unified inbox as source=community_forward items, and
// resolves them back when a site moderator decides locally.
//
// It differs from report intake in one deliberate way: `site` arrives in the
// request BODY, not from the client binding (a forwarder acts on behalf of many
// community-backed sites through one S2S identity). The KUN_TRUST_FORWARDER_CLIENT_IDS
// allowlist is the counterweight — only allow-listed clients may forward/resolve,
// and an empty allowlist means the face is entirely closed (fail-closed, ruling 3).
type ForwardService struct {
	db        *gorm.DB
	allowlist map[string]bool
}

// NewForwardService builds the service with the client-id allowlist. An empty /
// nil allowlist = the forward/resolve endpoints are always 403 (fail-closed).
func NewForwardService(db *gorm.DB, allowlist map[string]bool) *ForwardService {
	return &ForwardService{db: db, allowlist: allowlist}
}

func (s *ForwardService) allowed(clientID string) bool {
	return clientID != "" && s.allowlist[clientID]
}

// ForwardParams is a normalized forward request. CallerClientID is the
// authenticated S2S client id (allowlist gate); Site is taken from the body.
type ForwardParams struct {
	CallerClientID string
	Site           string
	SubjectKind    string
	SubjectID      string
	Severity       *int16
	WeightSum      *float32
	ContextNote    *string
}

// ForwardResult is the forward outcome: the review item and whether it was newly
// created (false = an open item already existed and its signals were updated).
type ForwardResult struct {
	ReviewItemID int64
	Created      bool
}

// Forward opens (or updates) a community_forward review item for a subject. It is
// idempotent against the single open item per subject (invariant 4): an existing
// open item takes the max weight_sum and fills its context_note only when empty;
// otherwise a fresh item is created.
func (s *ForwardService) Forward(ctx context.Context, p ForwardParams) (ForwardResult, error) {
	if !s.allowed(p.CallerClientID) {
		return ForwardResult{}, ErrForwarderNotAllowed
	}

	// Tenant fail-loud (invariant 11): the (site, kind) must be registered and
	// non-deprecated. Note this checks the BODY-supplied site — the allowlist,
	// not a client binding, is what scopes a forwarder here.
	var kindCount int64
	if err := s.db.WithContext(ctx).Model(&model.TrustSubjectKind{}).
		Where("site = ? AND key = ? AND is_deprecated = false", p.Site, p.SubjectKind).
		Count(&kindCount).Error; err != nil {
		return ForwardResult{}, err
	}
	if kindCount == 0 {
		return ForwardResult{}, ErrSubjectKindNotRegistered
	}

	var result ForwardResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// An open item already exists → update its signals, do not fork (ruling 1).
		var open model.TrustReviewItem
		lerr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("site = ? AND subject_kind = ? AND subject_id = ? AND status IN ?",
				p.Site, p.SubjectKind, p.SubjectID, []int16{model.ReviewStatusPending, model.ReviewStatusClaimed}).
			Limit(1).Take(&open).Error
		if lerr == nil {
			updates := map[string]any{}
			if p.WeightSum != nil {
				updates["report_weight_sum"] = gorm.Expr("GREATEST(COALESCE(report_weight_sum, 0), ?)", *p.WeightSum)
			}
			if p.ContextNote != nil {
				// Fill only when empty (ruling 5): a later forward never clobbers
				// the first evidence excerpt.
				updates["context_note"] = gorm.Expr(
					"CASE WHEN context_note IS NULL OR context_note = '' THEN ? ELSE context_note END", *p.ContextNote)
			}
			if len(updates) > 0 {
				if err := tx.Model(&model.TrustReviewItem{}).Where("id = ?", open.ID).Updates(updates).Error; err != nil {
					return err
				}
			}
			result = ForwardResult{ReviewItemID: open.ID, Created: false}
			return nil
		} else if !errors.Is(lerr, gorm.ErrRecordNotFound) {
			return lerr
		}

		item := model.TrustReviewItem{
			Site: p.Site, SubjectKind: p.SubjectKind, SubjectID: p.SubjectID,
			Source: model.ReviewSourceCommunityForward, Severity: p.Severity,
			ReportWeightSum: p.WeightSum, ContextNote: p.ContextNote,
			Priority: forwardPriority(p.Severity), Status: model.ReviewStatusPending,
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// A concurrent forward/report opened the item first (invariant 4
			// backstop) → adopt it instead of forking a second.
			if err := tx.Where("site = ? AND subject_kind = ? AND subject_id = ? AND status IN ?",
				p.Site, p.SubjectKind, p.SubjectID, []int16{model.ReviewStatusPending, model.ReviewStatusClaimed}).
				Limit(1).Take(&item).Error; err != nil {
				return err
			}
			result = ForwardResult{ReviewItemID: item.ID, Created: false}
			return nil
		}
		result = ForwardResult{ReviewItemID: item.ID, Created: true}
		return nil
	})
	if err != nil {
		return ForwardResult{}, err
	}
	return result, nil
}

// ResolveParams closes a review item on behalf of a site moderator's local
// decision. Outcome is "approved" (→ dismissed) or "rejected" (→ actioned).
type ResolveParams struct {
	CallerClientID string
	ReviewItemID   int64
	Outcome        string
	ActorRef       *string
}

// ResolveResult reports whether the resolve actually closed an open item
// (false = the item was already terminal — a tolerated race, ruling 1).
type ResolveResult struct {
	Closed bool
}

// Resolve closes an open item WITHOUT creating a disposition and WITHOUT any
// callback (ruling 1: the enforcement already happened on the site side, so a
// callback would echo back into an infinite loop). It appends one audit row
// (action=review_resolved_by_site). An already-terminal item → {Closed:false}.
func (s *ForwardService) Resolve(ctx context.Context, p ResolveParams) (ResolveResult, error) {
	if !s.allowed(p.CallerClientID) {
		return ResolveResult{}, ErrForwarderNotAllowed
	}
	if p.Outcome != "approved" && p.Outcome != "rejected" {
		return ResolveResult{}, ErrInvalidOutcome
	}

	var result ResolveResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.TrustReviewItem
		lerr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&item, p.ReviewItemID).Error
		if errors.Is(lerr, gorm.ErrRecordNotFound) {
			return ErrReviewItemNotFound
		}
		if lerr != nil {
			return lerr
		}
		if item.Status != model.ReviewStatusPending && item.Status != model.ReviewStatusClaimed {
			result = ResolveResult{Closed: false} // already terminal — idempotent
			return nil
		}

		status := model.ReviewStatusActioned // rejected → content removed on the site side
		if p.Outcome == "approved" {
			status = model.ReviewStatusDismissed // approved → kept
		}
		if err := tx.Model(&model.TrustReviewItem{}).Where("id = ?", p.ReviewItemID).
			Updates(map[string]any{
				"status":     status,
				"decided_at": time.Now(),
				// decided_by stays NULL: no trust operator acted (site-driven).
			}).Error; err != nil {
			return err
		}
		result = ResolveResult{Closed: true}
		return AppendAudit(tx, AuditEntry{
			Action: "review_resolved_by_site",
			Site:   strptr(item.Site), SubjectKind: strptr(item.SubjectKind), SubjectID: strptr(item.SubjectID),
			PolicyRef: p.ActorRef,
		})
	})
	if err != nil {
		return ResolveResult{}, err
	}
	return result, nil
}

// forwardPriority derives a queue priority for a forwarded item. Community
// forwards carry no severity of their own by default, so a base priority keeps
// them visible in the priority-ordered queue rather than sinking to 0.
func forwardPriority(severity *int16) float32 {
	if severity != nil {
		return float32(*severity)
	}
	return 1
}
