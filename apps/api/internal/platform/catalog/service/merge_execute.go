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

// mergeStmt is one statement of a rehang batch. collectWorks marks the ones
// whose RETURNING clause hands back the ids of the works whose read face the
// statement just changed — ExecuteMerge touches those works before it commits,
// otherwise the changes feed never learns that the merge rewrote them.
type mergeStmt struct {
	sql          string
	args         []any
	collectWorks bool
}

// rehangEntity repoints every child/reference of source onto target,
// deleting rows that would violate the target's unique constraints (the
// child already exists on the target — a true duplicate). The hook set per
// type is the pinned doc 10 §6.2-2 list. It returns the work ids whose
// rendered content changed, per the wave-118 touch matrix: a work renders
// credits[], its roster and its brand labels, so credit / character / label
// merges rewrite works that are not themselves part of the merge; person and
// org ids never reach the work face (they hang one level below the name and
// the label), so those merges touch nothing.
func rehangEntity(tx *gorm.DB, entityType int16, src, dst int64) ([]int64, error) {
	switch entityType {
	case model.EntityTypePerson:
		// The whole point of the name layer: person merge = repoint names.
		// person_id lives on the name face only — no work changes.
		return nil, tx.Exec(`UPDATE catalog_credit_name SET person_id = ? WHERE person_id = ?`, dst, src).Error

	case model.EntityTypeCreditName:
		stmts := []mergeStmt{
			// credits, deduped against uq_catalog_credit. Both statements
			// change what the host work lists under credits[] — the repoint
			// swaps the credited name, the dedup delete drops a row.
			{`UPDATE catalog_credit c SET credit_name_id = ? WHERE c.credit_name_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_credit d
			                     WHERE d.work_id = c.work_id AND d.credit_name_id = ?
			                       AND d.role_id = c.role_id
			                       AND COALESCE(d.character_id, 0) = COALESCE(c.character_id, 0))
			  RETURNING c.work_id`, []any{dst, src, dst}, true},
			{`DELETE FROM catalog_credit WHERE credit_name_id = ? RETURNING work_id`, []any{src}, true},
			// aliases, deduped against UNIQUE(owner, name, lang) — name face.
			{`UPDATE catalog_name_alias a SET credit_name_id = ? WHERE a.credit_name_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_name_alias b
			                     WHERE b.credit_name_id = ? AND b.name = a.name AND b.lang = a.lang)`, []any{dst, src, dst}, false},
			{`DELETE FROM catalog_name_alias WHERE credit_name_id = ?`, []any{src}, false},
			// primary-name references
			{`UPDATE catalog_person SET primary_credit_name_id = ? WHERE primary_credit_name_id = ?`, []any{dst, src}, false},
		}
		return execAll(tx, stmts)

	case model.EntityTypeOrg:
		// A work renders label ids, never the org behind them — org merge
		// changes the label face only.
		return nil, tx.Exec(`UPDATE catalog_label SET org_id = ? WHERE org_id = ?`, dst, src).Error

	case model.EntityTypeLabel:
		stmts := []mergeStmt{
			// label_id is not part of the credit unique key — plain repoint.
			{`UPDATE catalog_credit SET label_id = ? WHERE label_id = ? RETURNING work_id`, []any{dst, src}, true},
			{`UPDATE catalog_label_alias a SET label_id = ? WHERE a.label_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_label_alias b
			                     WHERE b.label_id = ? AND b.name = a.name AND b.lang = a.lang)`, []any{dst, src, dst}, false},
			{`DELETE FROM catalog_label_alias WHERE label_id = ?`, []any{src}, false},
			// brand edges, deduped against the (work, label, kind) PK
			{`UPDATE catalog_work_label e SET label_id = ? WHERE e.label_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_work_label x
			                     WHERE x.work_id = e.work_id AND x.label_id = ? AND x.kind = e.kind)
			  RETURNING e.work_id`, []any{dst, src, dst}, true},
			{`DELETE FROM catalog_work_label WHERE label_id = ? RETURNING work_id`, []any{src}, true},
		}
		return execAll(tx, stmts)

	case model.EntityTypeCharacter:
		stmts := []mergeStmt{
			// voice credits: character_id participates in uq_catalog_credit
			{`UPDATE catalog_credit c SET character_id = ? WHERE c.character_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_credit d
			                     WHERE d.work_id = c.work_id AND d.credit_name_id = c.credit_name_id
			                       AND d.role_id = c.role_id AND COALESCE(d.character_id, 0) = ?)
			  RETURNING c.work_id`, []any{dst, src, dst}, true},
			{`DELETE FROM catalog_credit WHERE character_id = ? RETURNING work_id`, []any{src}, true},
			{`UPDATE catalog_character_alias a SET character_id = ? WHERE a.character_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_character_alias b
			                     WHERE b.character_id = ? AND b.name = a.name AND b.lang = a.lang)`, []any{dst, src, dst}, false},
			{`DELETE FROM catalog_character_alias WHERE character_id = ?`, []any{src}, false},
			// roster edges (catalog_work_character, step 45 — added AFTER the
			// original hook list; step 49 regression fix). When both sides carry
			// an edge on the same work, the surviving edge folds in the loser's
			// stronger signal before the loser row is dropped: kind upgrades from
			// unknown (0) to the loser's typed value (doc 10 §6.2 — the more
			// specific value wins; two typed values keep the survivor's,
			// first-source-wins per step 45), and spoiler takes the higher of the
			// two so a source that flags a severe spoiler is never silently
			// downgraded. Then non-conflicting loser edges move, deduped against
			// uq_catalog_work_character (work_id, character_id), and the rest drop.
			//
			// The fold needs no RETURNING: it only fires on works that carry an
			// edge from BOTH sides, and every one of those loses the source's
			// edge to the dedup DELETE below, which collects them.
			{`UPDATE catalog_work_character d
			    SET kind = CASE WHEN d.kind = 0 THEN s.kind ELSE d.kind END,
			        spoiler = GREATEST(d.spoiler, s.spoiler),
			        updated_at = now()
			    FROM catalog_work_character s
			    WHERE d.character_id = ? AND s.character_id = ? AND s.work_id = d.work_id`, []any{dst, src}, false},
			{`UPDATE catalog_work_character e SET character_id = ?, updated_at = now() WHERE e.character_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_work_character x
			                     WHERE x.work_id = e.work_id AND x.character_id = ?)
			  RETURNING e.work_id`, []any{dst, src, dst}, true},
			{`DELETE FROM catalog_work_character WHERE character_id = ? RETURNING work_id`, []any{src}, true},
			// variant pointers follow the base character — character face only.
			{`UPDATE catalog_character SET instance_of = ? WHERE instance_of = ?`, []any{dst, src}, false},
		}
		return execAll(tx, stmts)

	case model.EntityTypeWork:
		// Everything below repoints a facet from source to target, so the
		// TARGET's own face changes (ExecuteMerge touches it unconditionally).
		// Relations are the exception that needs RETURNING: repointing an edge
		// rewrites the relations[] of the work at the OTHER end, which takes
		// no part in this merge.
		stmts := []mergeStmt{
			// edges between the pair would become self-edges (a<>b CHECK) —
			// drop them. Both endpoints are the merge pair itself, so there is
			// no third work to collect here.
			{`DELETE FROM catalog_work_relation
			   WHERE (a_work_id = ? AND b_work_id = ?) OR (a_work_id = ? AND b_work_id = ?)`, []any{src, dst, dst, src}, false},
			// a-side, deduped against the (a, b, type) PK
			{`UPDATE catalog_work_relation r SET a_work_id = ? WHERE r.a_work_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_work_relation x
			                     WHERE x.a_work_id = ? AND x.b_work_id = r.b_work_id
			                       AND x.relation_type_id = r.relation_type_id)
			  RETURNING r.b_work_id`, []any{dst, src, dst}, true},
			{`DELETE FROM catalog_work_relation WHERE a_work_id = ? RETURNING b_work_id`, []any{src}, true},
			// b-side
			{`UPDATE catalog_work_relation r SET b_work_id = ? WHERE r.b_work_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_work_relation x
			                     WHERE x.b_work_id = ? AND x.a_work_id = r.a_work_id
			                       AND x.relation_type_id = r.relation_type_id)
			  RETURNING r.a_work_id`, []any{dst, src, dst}, true},
			{`DELETE FROM catalog_work_relation WHERE b_work_id = ? RETURNING a_work_id`, []any{src}, true},
			// titles, deduped against UNIQUE(work, lang, title, kind)
			{`UPDATE catalog_work_title t SET work_id = ? WHERE t.work_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_work_title u
			                     WHERE u.work_id = ? AND u.lang = t.lang AND u.title = t.title AND u.kind = t.kind)`, []any{dst, src, dst}, false},
			{`DELETE FROM catalog_work_title WHERE work_id = ?`, []any{src}, false},
			// releases carry no unique on work_id — plain repoint
			{`UPDATE catalog_release SET work_id = ? WHERE work_id = ?`, []any{dst, src}, false},
			// credits, deduped against uq_catalog_credit
			{`UPDATE catalog_credit c SET work_id = ? WHERE c.work_id = ?
			    AND NOT EXISTS (SELECT 1 FROM catalog_credit d
			                     WHERE d.work_id = ? AND d.credit_name_id = c.credit_name_id
			                       AND d.role_id = c.role_id
			                       AND COALESCE(d.character_id, 0) = COALESCE(c.character_id, 0))`, []any{dst, src, dst}, false},
			{`DELETE FROM catalog_credit WHERE work_id = ?`, []any{src}, false},
		}
		return execAll(tx, stmts)
	}
	return nil, fmt.Errorf("catalog merge: unsupported entity type %d", entityType)
}

// mergeExternalRefs implements doc 10 §6.2-4: rows move to the target;
// identical (source_id, external_id) duplicates drop; when the target ends
// with competing EXACT ids on one source, ALL of them demote to probable —
// two silent exacts must never survive a merge.
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
		     AND source_id IN (SELECT source_id FROM catalog_external_ref
		                        WHERE entity_type = ? AND entity_id = ? AND link_kind = ?
		                        GROUP BY source_id HAVING COUNT(DISTINCT external_id) > 1)`,
			[]any{model.LinkKindProbable, entityType, dst, model.LinkKindExact, entityType, dst, model.LinkKindExact}, false},
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

// execAll runs a rehang batch in order and gathers the work ids returned by
// the statements flagged collectWorks. Those statements are queries (they carry
// a RETURNING clause), so they go through Raw().Scan rather than Exec.
func execAll(tx *gorm.DB, stmts []mergeStmt) ([]int64, error) {
	var touched []int64
	for _, s := range stmts {
		if !s.collectWorks {
			if err := tx.Exec(s.sql, s.args...).Error; err != nil {
				return nil, err
			}
			continue
		}
		var ids []int64
		if err := tx.Raw(s.sql, s.args...).Scan(&ids).Error; err != nil {
			return nil, err
		}
		touched = append(touched, ids...)
	}
	return touched, nil
}
