package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// coolingOffWindow is the mandatory delay between approval and execution of
// a destructive operation (doc 10 invariant 4) — admins are NOT exempt. A
// constant on purpose; configurability was ruled out for this step.
const coolingOffWindow = 48 * time.Hour

// MergeService owns the merge proposal lifecycle (doc 10 §6.1), the merge
// execution transaction (§6.2) and unmerge (§6.3).
type MergeService struct {
	db        *gorm.DB
	resolve   *ResolveService
	proposals *repository.ProposalRepository
	revisions *repository.RevisionRepository
}

func NewMergeService(db *gorm.DB, resolve *ResolveService,
	proposals *repository.ProposalRepository, revisions *repository.RevisionRepository) *MergeService {
	return &MergeService{db: db, resolve: resolve, proposals: proposals, revisions: revisions}
}

// ProposeMerge opens a merge proposal for source→target. Both ids are
// resolved first (proposing against an already-merged id must target its
// canonical successor); one open proposal per pair is enforced by the
// partial unique index.
func (s *MergeService) ProposeMerge(ctx context.Context, entityType int16, sourceID, targetID, proposedBy int64, note string) (*model.CatalogMergeProposal, error) {
	src, _, err := s.resolve.Resolve(ctx, entityType, sourceID)
	if err != nil {
		return nil, err
	}
	dst, _, err := s.resolve.Resolve(ctx, entityType, targetID)
	if err != nil {
		return nil, err
	}
	if src == dst {
		return nil, ErrSameEntity
	}
	// Merge endpoints must be live entities: proposing against a
	// soft-deleted (or nonexistent) row is a caller error, not a state to
	// silently carry to execution time.
	for _, id := range []int64{src, dst} {
		if err := assertEntityAlive(s.db.WithContext(ctx), entityType, id); err != nil {
			return nil, err
		}
	}
	p := &model.CatalogMergeProposal{
		EntityType:      entityType,
		SourceEntityID:  src,
		TargetEntityID:  dst,
		Status:          model.ProposalStatusOpen,
		FieldResolution: []byte(`{}`),
		ProposedBy:      proposedBy,
		Note:            note,
	}
	if err := s.db.WithContext(ctx).Create(p).Error; err != nil {
		if isUniqueViolation(err, "uq_catalog_merge_proposal_open") {
			return nil, ErrDuplicateOpenProposal
		}
		return nil, err
	}
	return p, nil
}

// ApproveMerge moves an open proposal to approved and starts the cooling-off
// clock: execute_after = now + 48h. The approver is recorded in the note
// trail (the table deliberately has no approved_by column yet).
func (s *MergeService) ApproveMerge(ctx context.Context, proposalID, approvedBy int64) error {
	return s.transition(ctx, proposalID, model.ProposalStatusOpen, func(tx *gorm.DB, p *model.CatalogMergeProposal) error {
		after := time.Now().Add(coolingOffWindow)
		return tx.Model(p).Updates(map[string]any{
			"status":        model.ProposalStatusApproved,
			"execute_after": after,
			"note":          appendNote(p.Note, fmt.Sprintf("approved by user %d", approvedBy)),
		}).Error
	})
}

// RejectMerge closes an open OR approved proposal as rejected. Approved is
// deliberately in scope: the 48h cooling-off window exists precisely so a
// merge can be vetoed after approval but before execution (step 97's #36138
// edition-split re-check is the motivating case); an executed proposal can
// never be rejected — that path is UnmergeEntity.
func (s *MergeService) RejectMerge(ctx context.Context, proposalID, rejectedBy int64, reason string) error {
	return s.transitionOneOf(ctx, proposalID, []int16{model.ProposalStatusOpen, model.ProposalStatusApproved}, func(tx *gorm.DB, p *model.CatalogMergeProposal) error {
		return tx.Model(p).Updates(map[string]any{
			"status": model.ProposalStatusRejected,
			"note":   appendNote(p.Note, fmt.Sprintf("rejected by user %d: %s", rejectedBy, reason)),
		}).Error
	})
}

// WithdrawMerge closes an open proposal as withdrawn (proposer's own path).
func (s *MergeService) WithdrawMerge(ctx context.Context, proposalID, withdrawnBy int64) error {
	return s.transition(ctx, proposalID, model.ProposalStatusOpen, func(tx *gorm.DB, p *model.CatalogMergeProposal) error {
		return tx.Model(p).Updates(map[string]any{
			"status": model.ProposalStatusWithdrawn,
			"note":   appendNote(p.Note, fmt.Sprintf("withdrawn by user %d", withdrawnBy)),
		}).Error
	})
}

// transitionOneOf runs fn on a FOR UPDATE-locked proposal after asserting its
// status is one of allowedStatuses.
func (s *MergeService) transitionOneOf(ctx context.Context, proposalID int64, allowedStatuses []int16, fn func(*gorm.DB, *model.CatalogMergeProposal) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := repository.LockProposal(tx, proposalID)
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("%w: proposal %d", ErrNotFound, proposalID)
		}
		for _, st := range allowedStatuses {
			if p.Status == st {
				return fn(tx, p)
			}
		}
		return fmt.Errorf("%w: proposal %d is in status %d", ErrProposalState, proposalID, p.Status)
	})
}

// transition runs fn on a FOR UPDATE-locked proposal after asserting its
// current status.
func (s *MergeService) transition(ctx context.Context, proposalID int64, requiredStatus int16, fn func(*gorm.DB, *model.CatalogMergeProposal) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := repository.LockProposal(tx, proposalID)
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("%w: proposal %d", ErrNotFound, proposalID)
		}
		if p.Status != requiredStatus {
			return fmt.Errorf("%w: proposal %d is in status %d", ErrProposalState, proposalID, p.Status)
		}
		return fn(tx, p)
	})
}

func appendNote(existing, entry string) string {
	if existing == "" {
		return entry
	}
	return existing + "\n" + entry
}

// isUniqueViolation reports whether err is a Postgres unique violation on
// the named index/constraint.
func isUniqueViolation(err error, name string) bool {
	return err != nil && strings.Contains(err.Error(), name)
}

// ErrEntityNotLive rejects merge endpoints that are soft-deleted or missing.
var ErrEntityNotLive = fmt.Errorf("catalog: entity is deleted or does not exist")

// assertEntityAlive verifies the entity row exists and is not soft-deleted.
// credit_name has no soft delete, so bare existence is the whole check there.
func assertEntityAlive(db *gorm.DB, entityType int16, id int64) error {
	table, ok := entityTableName(entityType)
	if !ok {
		return fmt.Errorf("catalog: unsupported entity type %d", entityType)
	}
	q := `SELECT count(*) FROM ` + table + ` WHERE id = ?`
	if entityType != model.EntityTypeCreditName {
		q += ` AND deleted_at IS NULL`
	}
	var count int64
	if err := db.Raw(q, id).Scan(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("%w: entity type %d id %d", ErrEntityNotLive, entityType, id)
	}
	return nil
}

// entityTableName maps entity type constants to base tables (the service-side
// twin of the repository lookup, kept here to avoid exporting it).
func entityTableName(entityType int16) (string, bool) {
	switch entityType {
	case model.EntityTypePerson:
		return "catalog_person", true
	case model.EntityTypeCreditName:
		return "catalog_credit_name", true
	case model.EntityTypeOrg:
		return "catalog_org", true
	case model.EntityTypeLabel:
		return "catalog_label", true
	case model.EntityTypeCharacter:
		return "catalog_character", true
	case model.EntityTypeWork:
		return "catalog_work", true
	}
	return "", false
}
