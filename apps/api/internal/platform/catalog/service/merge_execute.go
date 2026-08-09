package service

import (
	"context"
	"fmt"
	"time"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// ExecuteMerge runs an approved merge once its cooling-off window has
// elapsed, as ONE transaction following doc 10 §6.2 step by step:
//
//  1. field-level survivorship        (survivorship.go)
//  2. per-entity-type rehang          (rehang* below; alias/edge dedup folded in)
//  4. external_ref migration          (exact conflicts demote to probable)
//  5. usage row merge
//  6. redirect flatten + insert
//  7. revisions on BOTH sides
//  8. match-candidate cleanup for the pair
//
// then the source row is retired (soft delete; credit_name is the one hard
// delete — it has no soft-delete column, its history lives in the
// merged_source snapshot and its ID stays resolvable via the redirect).
func (s *MergeService) ExecuteMerge(ctx context.Context, proposalID int64, executedBy *int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := repository.LockProposal(tx, proposalID)
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("%w: proposal %d", ErrNotFound, proposalID)
		}
		if p.Status != model.ProposalStatusApproved {
			return fmt.Errorf("%w: proposal %d is in status %d, want approved", ErrProposalState, proposalID, p.Status)
		}
		if p.ExecuteAfter == nil || time.Now().Before(*p.ExecuteAfter) {
			return fmt.Errorf("%w: proposal %d executable after %v", ErrCoolingOff, proposalID, p.ExecuteAfter)
		}
		et, src, dst := p.EntityType, p.SourceEntityID, p.TargetEntityID

		// Both endpoints must still be live at execution time — an entity
		// deleted or merged away during the cooling-off window invalidates
		// the proposal.
		for _, id := range []int64{src, dst} {
			if err := assertEntityAlive(tx, et, id); err != nil {
				return err
			}
		}

		// Lock both entity rows in deterministic order (deadlock avoidance);
		// this also serializes revision numbering per entity.
		first, second := src, dst
		if second < first {
			first, second = second, first
		}
		if err := repository.LockEntityRow(tx, et, first); err != nil {
			return err
		}
		if err := repository.LockEntityRow(tx, et, second); err != nil {
			return err
		}

		// The source snapshot is taken BEFORE any mutation — it is the
		// unmerge rebuild source and must be pre-merge state.
		sourceSnap, err := takeSnapshot(tx, et, src)
		if err != nil {
			return fmt.Errorf("snapshot source: %w", err)
		}

		// (1) field-level survivorship.
		changed, err := applySurvivorship(tx, et, src, dst, p.FieldResolution)
		if err != nil {
			return fmt.Errorf("survivorship: %w", err)
		}

		// (2)+(3) rehang children onto the target, deduplicating against the
		// target's unique constraints. The statements that change what a work
		// renders hand their host work ids back (RETURNING) — those works are
		// touched at the end of this transaction.
		touched, err := rehangEntity(tx, et, src, dst)
		if err != nil {
			return fmt.Errorf("rehang: %w", err)
		}
		if et == model.EntityTypeWork {
			// A work merge always changes the target's read face: every facet
			// of the source now hangs off it, survivorship may have filled its
			// fields, and a claim may have moved onto it. The SOURCE is
			// deliberately never touched — retireSource takes it out of the
			// feed's predicate (status=merged plus soft delete) and the merge
			// signal for its id belongs to the redirects face.
			touched = append(touched, dst)
		}

		// (4) external refs move; same-source exact conflicts demote both
		// sides to probable — never two silent exacts. The demoted rows ARE
		// the human-review queue (the probable confirm bucket).
		if err := mergeExternalRefs(tx, et, src, dst); err != nil {
			return fmt.Errorf("external refs: %w", err)
		}

		// (5) usage rows.
		if err := repository.MergeUsage(tx, et, src, dst); err != nil {
			return fmt.Errorf("usage: %w", err)
		}

		// (6) redirect: flatten every chain ending at source, then record
		// source→target.
		if err := repository.FlattenRedirectsTo(tx, et, src, dst); err != nil {
			return err
		}
		if err := repository.InsertRedirect(tx, et, src, dst, executedBy, fmt.Sprintf("merge proposal %d", p.ID)); err != nil {
			return err
		}

		// (7) revisions on both sides. changed_fields records what
		// survivorship actually changed — collected during step 1, never
		// diffed after the fact.
		note := fmt.Sprintf("merge proposal %d", p.ID)
		if err := writeRevision(tx, et, src, model.RevisionActionMergedSource, sourceSnap, nil, executedBy, note); err != nil {
			return err
		}
		targetSnap, err := takeSnapshot(tx, et, dst)
		if err != nil {
			return fmt.Errorf("snapshot target: %w", err)
		}
		changedJSON, err := marshalChangedFields(changed)
		if err != nil {
			return err
		}
		if err := writeRevision(tx, et, dst, model.RevisionActionMergedTarget, targetSnap, changedJSON, executedBy, note); err != nil {
			return err
		}

		// (8) the pair's match candidate is settled by the merge itself.
		a, b := first, second
		if err := tx.Exec(`DELETE FROM catalog_match_candidate WHERE entity_type = ? AND a_id = ? AND b_id = ?`,
			et, a, b).Error; err != nil {
			return err
		}

		if err := retireSource(tx, et, src); err != nil {
			return fmt.Errorf("retire source: %w", err)
		}

		// The read face of these works changed as a side effect of the merge —
		// their own row was never written, so /v1/catalog/changes (an
		// (updated_at, id) keyset over catalog_work) would stay blind to it.
		// Same transaction as the merge: the news is atomic with the fact.
		if err := repository.TouchWorks(ctx, tx, touched); err != nil {
			return fmt.Errorf("touch works: %w", err)
		}

		return tx.Model(p).Updates(map[string]any{
			"status":      model.ProposalStatusExecuted,
			"executed_at": time.Now(),
		}).Error
	})
}

// mergeExternalRefs implements doc 10 §6.2-4: rows move to the target;
// identical (source_id, external_id) duplicates drop; when the target ends
// with competing EXACT ids on one source, ALL of them demote to probable —
// two silent exacts must never survive a merge.
//
// The galgame_wiki source is EXEMPT from that demotion (A2-0 §4c-1). Its rows
// are not upstream anchors at all: they are the wiki's own id map, the address
// book that keeps /galgame/<gid>, /galgame/official/<oid> and friends
// resolving after the galgame_* tables are dropped. Two wiki ids pointing at
// one entity is the NORMAL outcome of a merge, not a contradiction to review —
// both old URLs legitimately lead to the survivor, and the partial unique
// index is keyed by external_id so nothing collides. Demoting them would take
// both old URLs off the public face at once (exact-only crosses it), breaking
// the 100% redirect promise the rescue exists to keep. NOT IN (rather than
// <>) so a registry without the row demotes normally instead of silently
// exempting every source.
func mergeExternalRefs(tx *gorm.DB, entityType int16, src, dst int64) error {
	stmts := []mergeStmt{
		{`UPDATE catalog_external_ref r SET entity_id = ? WHERE r.entity_type = ? AND r.entity_id = ?
		    AND NOT EXISTS (SELECT 1 FROM catalog_external_ref t
		                     WHERE t.entity_type = r.entity_type AND t.entity_id = ?
		                       AND t.source_id = r.source_id AND t.external_id = r.external_id)`,
			[]any{dst, entityType, src, dst}, false},
		{`DELETE FROM catalog_external_ref WHERE entity_type = ? AND entity_id = ?`, []any{entityType, src}, false},
		{`UPDATE catalog_external_ref SET link_kind = ?
		   WHERE entity_type = ? AND entity_id = ? AND link_kind = ?
		     AND source_id NOT IN (SELECT id FROM catalog_source WHERE key IN ?)
		     AND source_id IN (SELECT source_id FROM catalog_external_ref
		                        WHERE entity_type = ? AND entity_id = ? AND link_kind = ?
		                        GROUP BY source_id HAVING COUNT(DISTINCT external_id) > 1)`,
			[]any{model.LinkKindProbable, entityType, dst, model.LinkKindExact, curatedSourceKeys,
				entityType, dst, model.LinkKindExact}, false},
	}
	// Refs are an identity face, not a work face: a work merge already touches
	// its target, and the other entity types never reach catalog_work here.
	_, err := execAll(tx, stmts)
	return err
}

// retireSource ends the source row's life: soft delete where the entity has
// a deleted_at column; credit_name (no soft delete by design) is hard
// deleted — its full state lives in the merged_source snapshot and its id
// resolves forever via the redirect. A merged work also frees its claim slot
// (the claim survives in the snapshot; the product row must re-claim via its
// anchors, which now resolve to the target).
func retireSource(tx *gorm.DB, entityType int16, src int64) error {
	switch entityType {
	case model.EntityTypePerson:
		return tx.Delete(&model.CatalogPerson{}, src).Error
	case model.EntityTypeCreditName:
		return tx.Exec(`DELETE FROM catalog_credit_name WHERE id = ?`, src).Error
	case model.EntityTypeLabel:
		return tx.Delete(&model.CatalogLabel{}, src).Error
	case model.EntityTypeCharacter:
		return tx.Delete(&model.CatalogCharacter{}, src).Error
	case model.EntityTypeWork:
		if err := tx.Exec(`UPDATE catalog_work SET status = ?, site = NULL, product_work_id = NULL WHERE id = ?`,
			model.WorkStatusMerged, src).Error; err != nil {
			return err
		}
		return tx.Delete(&model.CatalogWork{}, src).Error
	}
	return fmt.Errorf("catalog merge: unsupported entity type %d", entityType)
}
