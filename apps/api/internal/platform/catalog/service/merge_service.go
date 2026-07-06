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

// RejectMerge closes an open proposal as rejected.
func (s *MergeService) RejectMerge(ctx context.Context, proposalID, rejectedBy int64, reason string) error {
	return s.transition(ctx, proposalID, model.ProposalStatusOpen, func(tx *gorm.DB, p *model.CatalogMergeProposal) error {
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
