package service

import (
	"context"
	"fmt"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// AdminQueueService backs the human-review queue API (doc 17 §5): the
// candidate (ambiguity) bucket, the merge-proposal bucket and the probable-ref
// confirmation bucket. The auto-exact sampling and import-conflict buckets
// arrive with the ingestion steps.
type AdminQueueService struct {
	db    *gorm.DB
	merge *MergeService
}

func NewAdminQueueService(db *gorm.DB, merge *MergeService) *AdminQueueService {
	return &AdminQueueService{db: db, merge: merge}
}

// ErrExactTaken reports a probable→exact promotion losing to the
// anti-squatting line: another entity already holds the exact assertion.
var ErrExactTaken = fmt.Errorf("catalog: external identity is exact-linked to another entity")

// EntitySummary is the display brief attached to queue rows so a reviewer
// sees names, not bare ids. Soft-deleted entities still resolve (admin
// surfaces see below the veil).
type EntitySummary struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
}

// entitySummary looks up an entity's display name (credit_name uses name).
func entitySummary(db *gorm.DB, entityType int16, id int64) EntitySummary {
	table, ok := entityTableName(entityType)
	if !ok {
		return EntitySummary{ID: id}
	}
	column := "display_name"
	if entityType == model.EntityTypeCreditName {
		column = "name"
	}
	var name string
	// Best-effort: a missing row just leaves the name empty.
	_ = db.Raw(`SELECT `+column+` FROM `+table+` WHERE id = ?`, id).Scan(&name).Error
	return EntitySummary{ID: id, DisplayName: name}
}

// --- candidate bucket ---

// CandidateFilters narrows the candidate queue listing.
type CandidateFilters struct {
	Status     *int16
	EntityType *int16
	Reason     *int16
	Page       int
	Limit      int
}

// CandidateItem is one queue row plus both entities' briefs.
type CandidateItem struct {
	model.CatalogMatchCandidate
	A EntitySummary `json:"a"`
	B EntitySummary `json:"b"`
}

func (s *AdminQueueService) ListCandidates(ctx context.Context, f CandidateFilters) ([]CandidateItem, int64, error) {
	page, limit := normalizePage(f.Page, f.Limit)
	q := s.db.WithContext(ctx).Model(&model.CatalogMatchCandidate{})
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.EntityType != nil {
		q = q.Where("entity_type = ?", *f.EntityType)
	}
	if f.Reason != nil {
		q = q.Where("reason = ?", *f.Reason)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.CatalogMatchCandidate
	if err := q.Order("created_at, entity_type, a_id, b_id").
		Offset((page - 1) * limit).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]CandidateItem, len(rows))
	for i, row := range rows {
		items[i] = CandidateItem{
			CatalogMatchCandidate: row,
			A:                     entitySummary(s.db.WithContext(ctx).Unscoped(), row.EntityType, row.AID),
			B:                     entitySummary(s.db.WithContext(ctx).Unscoped(), row.EntityType, row.BID),
		}
	}
	return items, total, nil
}

// CandidateDecision is one reviewer verdict on a candidate pair.
type CandidateDecision struct {
	EntityType int16
	AID, BID   int64
	// Action: "accept" | "reject" | "defer".
	Action string
	// Accept only: which side is absorbed into which (the candidate pair is
	// unordered; the merge direction is the reviewer's call).
	SourceID, TargetID int64
	Note               string
	DecidedBy          int64
}

// DecideCandidate applies a reviewer verdict. accept additionally opens a
// merge proposal for the pair (the candidate graduates into the proposal
// bucket). Rejected candidates keep their row forever — that permanence is
// what stops the same pair from resurfacing on every import; it is
// deliberately NOT a catalog_match_rejection row (that table is negative
// knowledge about external-ref assertions only — the two must never mix).
func (s *AdminQueueService) DecideCandidate(ctx context.Context, d CandidateDecision) (*model.CatalogMergeProposal, error) {
	var status int16
	switch d.Action {
	case "accept":
		status = model.CandidateStatusAccepted
	case "reject":
		status = model.CandidateStatusRejected
	case "defer":
		status = model.CandidateStatusDeferred
	default:
		return nil, fmt.Errorf("%w: unknown action %q", ErrProposalState, d.Action)
	}

	// Validate the candidate first (found + still undecided).
	var cand model.CatalogMatchCandidate
	err := s.db.WithContext(ctx).Raw(`SELECT * FROM catalog_match_candidate
	                WHERE entity_type = ? AND a_id = ? AND b_id = ?`,
		d.EntityType, d.AID, d.BID).Scan(&cand).Error
	if err != nil {
		return nil, err
	}
	if cand.AID == 0 { // ids start at 1: a zero AID means no row was scanned
		return nil, fmt.Errorf("%w: candidate (%d, %d, %d)", ErrNotFound, d.EntityType, d.AID, d.BID)
	}
	if cand.Status != model.CandidateStatusPending && cand.Status != model.CandidateStatusDeferred {
		return nil, fmt.Errorf("%w: candidate already decided (status %d)", ErrProposalState, cand.Status)
	}

	// Accept opens the proposal BEFORE the status flips: a propose failure
	// (duplicate open proposal, dead endpoint, ...) must leave the candidate
	// still pending, never "accepted with no proposal".
	var proposal *model.CatalogMergeProposal
	if d.Action == "accept" {
		// The merge direction comes from the reviewer; both ids must belong
		// to the pair.
		if !(d.SourceID == d.AID && d.TargetID == d.BID) && !(d.SourceID == d.BID && d.TargetID == d.AID) {
			return nil, fmt.Errorf("%w: source/target must be the candidate pair", ErrProposalState)
		}
		proposal, err = s.merge.ProposeMerge(ctx, d.EntityType, d.SourceID, d.TargetID, d.DecidedBy, d.Note)
		if err != nil {
			return nil, err
		}
	}

	res := s.db.WithContext(ctx).Model(&model.CatalogMatchCandidate{}).
		Where("entity_type = ? AND a_id = ? AND b_id = ? AND status IN ?",
			d.EntityType, d.AID, d.BID,
			[]int16{model.CandidateStatusPending, model.CandidateStatusDeferred}).
		Updates(map[string]any{"status": status, "decided_by": d.DecidedBy, "decided_at": time.Now()})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		// Raced with another reviewer after the proposal was opened — the
		// proposal stays (withdrawable), the other verdict wins the row.
		return proposal, fmt.Errorf("%w: candidate decided concurrently", ErrProposalState)
	}
	return proposal, nil
}

// --- proposal bucket ---

// ProposalFilters narrows the proposal queue listing.
type ProposalFilters struct {
	Status     *int16
	EntityType *int16
	Page       int
	Limit      int
}

// GetProposal returns one proposal by id (nil when missing).
func (s *AdminQueueService) GetProposal(ctx context.Context, id int64) (*model.CatalogMergeProposal, error) {
	var row model.CatalogMergeProposal
	err := s.db.WithContext(ctx).First(&row, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ProposalItem is one proposal plus both entities' briefs.
type ProposalItem struct {
	model.CatalogMergeProposal
	Source EntitySummary `json:"source"`
	Target EntitySummary `json:"target"`
}

func (s *AdminQueueService) ListProposals(ctx context.Context, f ProposalFilters) ([]ProposalItem, int64, error) {
	page, limit := normalizePage(f.Page, f.Limit)
	q := s.db.WithContext(ctx).Model(&model.CatalogMergeProposal{})
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.EntityType != nil {
		q = q.Where("entity_type = ?", *f.EntityType)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.CatalogMergeProposal
	if err := q.Order("proposed_at, id").
		Offset((page - 1) * limit).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]ProposalItem, len(rows))
	for i, row := range rows {
		items[i] = ProposalItem{
			CatalogMergeProposal: row,
			Source:               entitySummary(s.db.WithContext(ctx).Unscoped(), row.EntityType, row.SourceEntityID),
			Target:               entitySummary(s.db.WithContext(ctx).Unscoped(), row.EntityType, row.TargetEntityID),
		}
	}
	return items, total, nil
}

// --- probable-ref confirmation bucket ---

// RefFilters narrows the probable-ref listing.
type RefFilters struct {
	SourceID   *int16
	EntityType *int16
	Page       int
	Limit      int
}

// ProbableRefItem is one unconfirmed probable assertion plus its entity brief.
type ProbableRefItem struct {
	model.CatalogExternalRef
	Entity EntitySummary `json:"entity"`
}

// ListProbableRefs returns the confirmation bucket: link_kind=probable AND
// not yet verified. That predicate NATURALLY includes the rows a merge
// demoted from exact (they keep their original matched_by and have no
// verified_at) — no special-casing needed (step-05 carry-over ②).
func (s *AdminQueueService) ListProbableRefs(ctx context.Context, f RefFilters) ([]ProbableRefItem, int64, error) {
	page, limit := normalizePage(f.Page, f.Limit)
	q := s.db.WithContext(ctx).Model(&model.CatalogExternalRef{}).
		Where("link_kind = ? AND verified_at IS NULL", model.LinkKindProbable)
	if f.SourceID != nil {
		q = q.Where("source_id = ?", *f.SourceID)
	}
	if f.EntityType != nil {
		q = q.Where("entity_type = ?", *f.EntityType)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.CatalogExternalRef
	if err := q.Order("created_at, entity_type, entity_id, source_id, external_id").
		Offset((page - 1) * limit).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]ProbableRefItem, len(rows))
	for i, row := range rows {
		items[i] = ProbableRefItem{
			CatalogExternalRef: row,
			Entity:             entitySummary(s.db.WithContext(ctx).Unscoped(), row.EntityType, row.EntityID),
		}
	}
	return items, total, nil
}

// RefKey addresses one external-ref assertion.
type RefKey struct {
	EntityType int16
	EntityID   int64
	SourceID   int16
	ExternalID string
}

// ConfirmRef promotes a probable assertion to exact (human confirmation, the
// only path up — doc 17 R8). Losing to the exact partial unique returns
// ErrExactTaken with the current holder attached.
func (s *AdminQueueService) ConfirmRef(ctx context.Context, key RefKey, verifiedBy int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ref model.CatalogExternalRef
		err := tx.Raw(`SELECT * FROM catalog_external_ref
		                WHERE entity_type = ? AND entity_id = ? AND source_id = ? AND external_id = ? FOR UPDATE`,
			key.EntityType, key.EntityID, key.SourceID, key.ExternalID).Scan(&ref).Error
		if err != nil {
			return err
		}
		if ref.ExternalID == "" {
			return fmt.Errorf("%w: external ref", ErrNotFound)
		}
		if ref.LinkKind != model.LinkKindProbable {
			return fmt.Errorf("%w: ref is not probable (kind %d)", ErrProposalState, ref.LinkKind)
		}
		now := time.Now()
		err = tx.Model(&model.CatalogExternalRef{}).
			Where("entity_type = ? AND entity_id = ? AND source_id = ? AND external_id = ?",
				key.EntityType, key.EntityID, key.SourceID, key.ExternalID).
			Updates(map[string]any{"link_kind": model.LinkKindExact, "verified_by": verifiedBy, "verified_at": now}).Error
		if err != nil {
			if isUniqueViolation(err, "uq_catalog_external_ref_exact") {
				var holder int64
				_ = tx.Raw(`SELECT entity_id FROM catalog_external_ref
				             WHERE source_id = ? AND external_id = ? AND entity_type = ? AND link_kind = ?`,
					key.SourceID, key.ExternalID, key.EntityType, model.LinkKindExact).Scan(&holder).Error
				return fmt.Errorf("%w: held by entity %d", ErrExactTaken, holder)
			}
			return err
		}
		return nil
	})
}

// RejectRef removes a wrong assertion and records it as first-class negative
// knowledge: the (entity, source, external_id) pairing lands in
// catalog_match_rejection with a mandatory reason, so no future import
// re-adds it.
func (s *AdminQueueService) RejectRef(ctx context.Context, key RefKey, reason string, rejectedBy int64) error {
	if reason == "" {
		return fmt.Errorf("%w: rejection reason is required", ErrProposalState)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`DELETE FROM catalog_external_ref
		                 WHERE entity_type = ? AND entity_id = ? AND source_id = ? AND external_id = ?`,
			key.EntityType, key.EntityID, key.SourceID, key.ExternalID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("%w: external ref", ErrNotFound)
		}
		return tx.Create(&model.CatalogMatchRejection{
			EntityType: key.EntityType,
			EntityID:   key.EntityID,
			SourceID:   key.SourceID,
			ExternalID: key.ExternalID,
			Reason:     reason,
			RejectedBy: &rejectedBy,
		}).Error
	})
}

func normalizePage(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	return page, limit
}
