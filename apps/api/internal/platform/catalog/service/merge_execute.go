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
		// target's unique constraints.
		if err := rehangEntity(tx, et, src, dst); err != nil {
			return fmt.Errorf("rehang: %w", err)
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

		return tx.Model(p).Updates(map[string]any{
			"status":      model.ProposalStatusExecuted,
			"executed_at": time.Now(),
		}).Error
	})
}

// rehangEntity repoints every child/reference of source onto target,
// deleting rows that would violate the target's unique constraints (the
// child already exists on the target — a true duplicate). The hook set per
// type is the pinned doc 10 §6.2-2 list.
func rehangEntity(tx *gorm.DB, entityType int16, src, dst int64) error {
	switch entityType {
	case model.EntityTypePerson:
		// The whole point of the name layer: person merge = repoint names.
		return tx.Exec(`UPDATE catalog_credit_name SET person_id = ? WHERE person_id = ?`, dst, src).Error

	case model.EntityTypeCreditName:
		stmts := []struct {
			sql  string
			args []any
		}{
			// credits, deduped against uq_catalog_credit
			{`UPDATE catalog_credit c SET credit_name_id = ? WHERE c.credit_name_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_credit d
			                     WHERE d.work_id = c.work_id AND d.credit_name_id = ?
			                       AND d.role_id = c.role_id
			                       AND COALESCE(d.character_id, 0) = COALESCE(c.character_id, 0))`, []any{dst, src, dst}},
			{`DELETE FROM catalog_credit WHERE credit_name_id = ?`, []any{src}},
			// aliases, deduped against UNIQUE(owner, name, lang)
			{`UPDATE catalog_name_alias a SET credit_name_id = ? WHERE a.credit_name_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_name_alias b
			                     WHERE b.credit_name_id = ? AND b.name = a.name AND b.lang = a.lang)`, []any{dst, src, dst}},
			{`DELETE FROM catalog_name_alias WHERE credit_name_id = ?`, []any{src}},
			// primary-name references
			{`UPDATE catalog_person SET primary_credit_name_id = ? WHERE primary_credit_name_id = ?`, []any{dst, src}},
		}
		return execAll(tx, stmts)

	case model.EntityTypeOrg:
		return tx.Exec(`UPDATE catalog_label SET org_id = ? WHERE org_id = ?`, dst, src).Error

	case model.EntityTypeLabel:
		stmts := []struct {
			sql  string
			args []any
		}{
			// label_id is not part of the credit unique key — plain repoint.
			{`UPDATE catalog_credit SET label_id = ? WHERE label_id = ?`, []any{dst, src}},
			{`UPDATE catalog_label_alias a SET label_id = ? WHERE a.label_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_label_alias b
			                     WHERE b.label_id = ? AND b.name = a.name AND b.lang = a.lang)`, []any{dst, src, dst}},
			{`DELETE FROM catalog_label_alias WHERE label_id = ?`, []any{src}},
		}
		return execAll(tx, stmts)

	case model.EntityTypeCharacter:
		stmts := []struct {
			sql  string
			args []any
		}{
			// voice credits: character_id participates in uq_catalog_credit
			{`UPDATE catalog_credit c SET character_id = ? WHERE c.character_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_credit d
			                     WHERE d.work_id = c.work_id AND d.credit_name_id = c.credit_name_id
			                       AND d.role_id = c.role_id AND COALESCE(d.character_id, 0) = ?)`, []any{dst, src, dst}},
			{`DELETE FROM catalog_credit WHERE character_id = ?`, []any{src}},
			{`UPDATE catalog_character_alias a SET character_id = ? WHERE a.character_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_character_alias b
			                     WHERE b.character_id = ? AND b.name = a.name AND b.lang = a.lang)`, []any{dst, src, dst}},
			{`DELETE FROM catalog_character_alias WHERE character_id = ?`, []any{src}},
			// variant pointers follow the base character
			{`UPDATE catalog_character SET instance_of = ? WHERE instance_of = ?`, []any{dst, src}},
		}
		return execAll(tx, stmts)

	case model.EntityTypeWork:
		stmts := []struct {
			sql  string
			args []any
		}{
			// edges between the pair would become self-edges (a<>b CHECK) — drop them
			{`DELETE FROM catalog_work_relation
			   WHERE (a_work_id = ? AND b_work_id = ?) OR (a_work_id = ? AND b_work_id = ?)`, []any{src, dst, dst, src}},
			// a-side, deduped against the (a, b, type) PK
			{`UPDATE catalog_work_relation r SET a_work_id = ? WHERE r.a_work_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_work_relation x
			                     WHERE x.a_work_id = ? AND x.b_work_id = r.b_work_id
			                       AND x.relation_type_id = r.relation_type_id)`, []any{dst, src, dst}},
			{`DELETE FROM catalog_work_relation WHERE a_work_id = ?`, []any{src}},
			// b-side
			{`UPDATE catalog_work_relation r SET b_work_id = ? WHERE r.b_work_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_work_relation x
			                     WHERE x.b_work_id = ? AND x.a_work_id = r.a_work_id
			                       AND x.relation_type_id = r.relation_type_id)`, []any{dst, src, dst}},
			{`DELETE FROM catalog_work_relation WHERE b_work_id = ?`, []any{src}},
			// titles, deduped against UNIQUE(work, lang, title, kind)
			{`UPDATE catalog_work_title t SET work_id = ? WHERE t.work_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_work_title u
			                     WHERE u.work_id = ? AND u.lang = t.lang AND u.title = t.title AND u.kind = t.kind)`, []any{dst, src, dst}},
			{`DELETE FROM catalog_work_title WHERE work_id = ?`, []any{src}},
			// releases carry no unique on work_id — plain repoint
			{`UPDATE catalog_release SET work_id = ? WHERE work_id = ?`, []any{dst, src}},
			// credits, deduped against uq_catalog_credit
			{`UPDATE catalog_credit c SET work_id = ? WHERE c.work_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_credit d
			                     WHERE d.work_id = ? AND d.credit_name_id = c.credit_name_id
			                       AND d.role_id = c.role_id
			                       AND COALESCE(d.character_id, 0) = COALESCE(c.character_id, 0))`, []any{dst, src, dst}},
			{`DELETE FROM catalog_credit WHERE work_id = ?`, []any{src}},
		}
		return execAll(tx, stmts)
	}
	return fmt.Errorf("catalog merge: unsupported entity type %d", entityType)
}

// mergeExternalRefs implements doc 10 §6.2-4: rows move to the target;
// identical (source_id, external_id) duplicates drop; when the target ends
// with competing EXACT ids on one source, ALL of them demote to probable —
// two silent exacts must never survive a merge.
func mergeExternalRefs(tx *gorm.DB, entityType int16, src, dst int64) error {
	stmts := []struct {
		sql  string
		args []any
	}{
		{`UPDATE catalog_external_ref r SET entity_id = ? WHERE r.entity_type = ? AND r.entity_id = ?
		    AND NOT EXISTS (SELECT 1 FROM catalog_external_ref t
		                     WHERE t.entity_type = r.entity_type AND t.entity_id = ?
		                       AND t.source_id = r.source_id AND t.external_id = r.external_id)`,
			[]any{dst, entityType, src, dst}},
		{`DELETE FROM catalog_external_ref WHERE entity_type = ? AND entity_id = ?`, []any{entityType, src}},
		{`UPDATE catalog_external_ref SET link_kind = ?
		   WHERE entity_type = ? AND entity_id = ? AND link_kind = ?
		     AND source_id IN (SELECT source_id FROM catalog_external_ref
		                        WHERE entity_type = ? AND entity_id = ? AND link_kind = ?
		                        GROUP BY source_id HAVING COUNT(DISTINCT external_id) > 1)`,
			[]any{model.LinkKindProbable, entityType, dst, model.LinkKindExact, entityType, dst, model.LinkKindExact}},
	}
	return execAll(tx, stmts)
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
	case model.EntityTypeOrg:
		return tx.Delete(&model.CatalogOrg{}, src).Error
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

func execAll(tx *gorm.DB, stmts []struct {
	sql  string
	args []any
}) error {
	for _, s := range stmts {
		if err := tx.Exec(s.sql, s.args...).Error; err != nil {
			return err
		}
	}
	return nil
}
