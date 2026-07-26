package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"

	"gorm.io/gorm"
)

// entityTypeOf maps a class label to its EntityType* constant. Both the
// person-anchored (step 49) and the orphan (step 50) credit-name classes are
// credit_name entities.
func entityTypeOf(class string) int16 {
	if class == classCreditName || class == classOrphanCreditName || class == classMixedCreditName {
		return model.EntityTypeCreditName
	}
	return model.EntityTypeCharacter
}

// runDetect is the dry report: every class's group counts, the guard counters,
// and a handful of samples (with the trigger groups called out explicitly).
func runDetect(db *gorm.DB, w io.Writer) error {
	charGroups, cs, err := detectCharacters(db)
	if err != nil {
		return err
	}
	creditGroups, ns, err := detectCreditNames(db)
	if err != nil {
		return err
	}
	orphanGroups, os, err := detectOrphanCreditNames(db)
	if err != nil {
		return err
	}
	mixedGroups, ms, err := detectMixedCreditNames(db)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "[detect] character: groups=%d pairs=%d  (skipped: dirty_buckets=%d bridged_components=%d)\n",
		cs.charGroups, cs.charPairs, cs.charDirtyBkt, cs.charBridged)
	fmt.Fprintf(w, "[detect] credit_name: groups=%d pairs=%d\n", ns.creditGroups, ns.creditPairs)
	fmt.Fprintf(w, "[detect] orphan-creditname: groups=%d pairs=%d  (skipped: dirty_buckets=%d bridged_components=%d)\n",
		os.orphanGroups, os.orphanPairs, os.orphanDirtyBkt, os.orphanBridged)
	fmt.Fprintf(w, "[detect] mixed-creditname: groups=%d pairs=%d  (skipped: dirty_buckets=%d bridged_components=%d frozen=%d)\n",
		ms.mixedGroups, ms.mixedPairs, ms.mixedDirtyBkt, ms.mixedBridged, ms.mixedFrozen)

	fmt.Fprintln(w, "  character samples:")
	printSamples(w, charGroups, 5)
	fmt.Fprintln(w, "  credit_name samples:")
	printSamples(w, creditGroups, 5)
	fmt.Fprintln(w, "  orphan-creditname samples:")
	printSamples(w, orphanGroups, 5)
	fmt.Fprintln(w, "  mixed-creditname samples:")
	printSamples(w, mixedGroups, 5)
	// The step-50 trigger group must be visibly in the candidate set.
	printOrphanGroupContaining(w, orphanGroups, 14695, 47429)
	return nil
}

// printOrphanGroupContaining surfaces the group that unifies two known credit
// name ids (the 藤咲ウサ EG↔DLsite trigger), proving it is a candidate.
func printOrphanGroupContaining(w io.Writer, groups []mergeGroup, a, b int64) {
	for _, g := range groups {
		ids := append([]int64{g.survivor}, g.sources...)
		var seenA, seenB bool
		for _, id := range ids {
			seenA = seenA || id == a
			seenB = seenB || id == b
		}
		if seenA && seenB {
			fmt.Fprintf(w, "  trigger group (%d↔%d): %s\n", a, b, g.sample)
			return
		}
	}
	fmt.Fprintf(w, "  trigger group (%d↔%d): NOT FOUND\n", a, b)
}

func printSamples(w io.Writer, groups []mergeGroup, n int) {
	for i, g := range groups {
		if i >= n {
			break
		}
		fmt.Fprintf(w, "    %s %s\n", g.class, g.sample)
	}
}

// proposeStats is the write-side tally.
type proposeStats struct {
	groups, pairs, proposals, approved, skipped, errs int
}

// runPropose detects, then for every (survivor, source) opens a merge proposal
// and approves it — starting the mandatory 48h cooling clock. It NEVER shortens
// that window (invariant 4): execution is a separate cooled pass. -limit caps
// the number of GROUPS (a canary), -class filters which class to drive. Each
// proposal is tagged with its class's wave note (noteTagFor) so -mode execute
// addresses exactly one wave.
func runPropose(ctx context.Context, db *gorm.DB, w io.Writer, merge *service.MergeService,
	actor int64, class string, limit int, run bool) error {
	groups, err := collectGroups(db, class)
	if err != nil {
		return err
	}
	if limit > 0 && limit < len(groups) {
		groups = groups[:limit]
	}

	var st proposeStats
	for _, g := range groups {
		st.groups++
		et := entityTypeOf(g.class)
		note := noteTagFor(g.class)
		for _, src := range g.sources {
			st.pairs++
			if !run {
				st.proposals++
				continue
			}
			p, err := merge.ProposeMerge(ctx, et, src, g.survivor, actor, note)
			if err != nil {
				// Already merged (resolves to target) or already proposed: the
				// idempotent no-op path, not an error.
				if errors.Is(err, service.ErrSameEntity) || errors.Is(err, service.ErrDuplicateOpenProposal) {
					st.skipped++
					continue
				}
				fmt.Fprintf(w, "  %s %d→%d: propose ERROR %v\n", g.class, src, g.survivor, err)
				st.errs++
				continue
			}
			if err := merge.ApproveMerge(ctx, p.ID, actor); err != nil {
				fmt.Fprintf(w, "  %s proposal %d: approve ERROR %v\n", g.class, p.ID, err)
				st.errs++
				continue
			}
			st.proposals++
			st.approved++
		}
	}
	mode := "DRY-RUN (pass -run to propose+approve)"
	if run {
		mode = "APPLIED"
	}
	fmt.Fprintf(w, "%s [propose] class=%s groups=%d pairs=%d proposals=%d approved=%d skipped=%d errors=%d\n",
		mode, class, st.groups, st.pairs, st.proposals, st.approved, st.skipped, st.errs)
	if st.errs > 0 {
		return fmt.Errorf("%d pairs failed to propose/approve", st.errs)
	}
	return nil
}

// collectGroups returns the requested class(es) of dedup groups. "both" is the
// step-49 pair (character + person-anchored credit_name); the step-50 orphan
// class is a deliberately separate wave (its own note tag) reached only by
// -class orphan-creditname.
func collectGroups(db *gorm.DB, class string) ([]mergeGroup, error) {
	var out []mergeGroup
	if class == classCharacter || class == "both" {
		g, _, err := detectCharacters(db)
		if err != nil {
			return nil, err
		}
		out = append(out, g...)
	}
	if class == classCreditName || class == "both" {
		g, _, err := detectCreditNames(db)
		if err != nil {
			return nil, err
		}
		out = append(out, g...)
	}
	if class == classOrphanCreditName {
		g, _, err := detectOrphanCreditNames(db)
		if err != nil {
			return nil, err
		}
		out = append(out, g...)
	}
	if class == classMixedCreditName {
		g, _, err := detectMixedCreditNames(db)
		if err != nil {
			return nil, err
		}
		out = append(out, g...)
	}
	return out, nil
}

// runExecute executes this batch's cooled, approved proposals (addressed by the
// note tag, both classes). The cooling window is enforced by ExecuteMerge —
// this only selects rows already past execute_after. -limit 1 first (canary).
func runExecute(ctx context.Context, db *gorm.DB, w io.Writer, merge *service.MergeService,
	actor int64, note string, limit int, run bool) error {
	var ids []int64
	q := `SELECT id FROM catalog_merge_proposal
		WHERE status = ? AND execute_after <= now() AND note LIKE ?
		  AND entity_type IN (?, ?)
		ORDER BY id`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	if err := db.Raw(q, model.ProposalStatusApproved, "%"+note+"%",
		model.EntityTypeCharacter, model.EntityTypeCreditName).Scan(&ids).Error; err != nil {
		return err
	}
	var executed, errs int
	for _, id := range ids {
		if !run {
			executed++
			continue
		}
		if err := merge.ExecuteMerge(ctx, id, &actor); err != nil {
			fmt.Fprintf(w, "  proposal %d: execute ERROR %v\n", id, err)
			errs++
			continue
		}
		executed++
	}
	mode := "DRY-RUN (pass -run to execute)"
	if run {
		mode = "APPLIED"
	}
	fmt.Fprintf(w, "%s [execute] cooled=%d executed=%d errors=%d\n", mode, len(ids), executed, errs)
	if errs > 0 {
		return fmt.Errorf("%d executions failed", errs)
	}
	return nil
}

// cleanupScope is the class-B predicate (step 50): a voice-actor credit with
// NO character that shares its (work, credit_name, role) with a
// character-BEARING credit — the empty one is strictly-less information, a
// byproduct of step 13 importing EG's two lists (staff 声優 + appearance 出演).
// The EXISTS clause is also the preservation boundary: a voice actor credited
// on a work with ONLY an empty-role credit (no character-bearing peer) is a
// sole attribution and is left alone. Must run AFTER class A so the two source
// lists are already unified under one credit_name.
const cleanupScope = `e.character_id IS NULL
	AND e.role_id = (SELECT id FROM catalog_role WHERE key = 'voice-actor')
	AND EXISTS (SELECT 1 FROM catalog_credit d
	             WHERE d.work_id = e.work_id AND d.credit_name_id = e.credit_name_id
	               AND d.role_id = e.role_id AND d.character_id IS NOT NULL)`

// runCleanup performs class B: it DELETEs the redundant empty-role voice
// credits (not a merge — the row is not an entity). It follows the merge
// machine's own credit-dedup path, a plain DELETE with no per-row revision
// (catalog credit deletes are not versioned). Dry (default) reports the count
// and samples; -run deletes.
func runCleanup(db *gorm.DB, w io.Writer, run bool) error {
	var redundant int64
	if err := db.Raw(`SELECT count(*) FROM catalog_credit e WHERE ` + cleanupScope).Row().Scan(&redundant); err != nil {
		return err
	}
	var samples []struct {
		ID           int64  `gorm:"column:id"`
		WorkID       int64  `gorm:"column:work_id"`
		CreditNameID int64  `gorm:"column:credit_name_id"`
		Name         string `gorm:"column:name"`
	}
	if err := db.Raw(`SELECT e.id, e.work_id, e.credit_name_id, cn.name
		FROM catalog_credit e JOIN catalog_credit_name cn ON cn.id = e.credit_name_id
		WHERE ` + cleanupScope + `
		ORDER BY e.work_id, e.id LIMIT 5`).Scan(&samples).Error; err != nil {
		return err
	}
	fmt.Fprintf(w, "[cleanup] redundant empty-role VA credits: %d\n", redundant)
	for _, s := range samples {
		fmt.Fprintf(w, "    credit id=%d work=%d cn=%d name=%q\n", s.ID, s.WorkID, s.CreditNameID, s.Name)
	}
	if !run {
		fmt.Fprintf(w, "DRY-RUN (pass -run to delete) [cleanup] would_delete=%d\n", redundant)
		return nil
	}
	res := db.Exec(`DELETE FROM catalog_credit e WHERE ` + cleanupScope)
	if res.Error != nil {
		return res.Error
	}
	fmt.Fprintf(w, "APPLIED [cleanup] deleted=%d\n", res.RowsAffected)
	return nil
}
