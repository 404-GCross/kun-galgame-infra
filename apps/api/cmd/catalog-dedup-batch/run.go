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

// entityTypeOf maps a class label to its EntityType* constant.
func entityTypeOf(class string) int16 {
	if class == classCreditName {
		return model.EntityTypeCreditName
	}
	return model.EntityTypeCharacter
}

// runDetect is the dry report: both classes' group counts, the guard counters,
// and a handful of samples (with the two trigger groups called out explicitly).
func runDetect(db *gorm.DB, w io.Writer) error {
	charGroups, cs, err := detectCharacters(db)
	if err != nil {
		return err
	}
	creditGroups, ns, err := detectCreditNames(db)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "[detect] character: groups=%d pairs=%d  (skipped: dirty_buckets=%d bridged_components=%d)\n",
		cs.charGroups, cs.charPairs, cs.charDirtyBkt, cs.charBridged)
	fmt.Fprintf(w, "[detect] credit_name: groups=%d pairs=%d\n", ns.creditGroups, ns.creditPairs)

	fmt.Fprintln(w, "  character samples:")
	printSamples(w, charGroups, 5)
	fmt.Fprintln(w, "  credit_name samples:")
	printSamples(w, creditGroups, 5)
	return nil
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
// the number of GROUPS (a canary), -class filters which class to drive.
func runPropose(ctx context.Context, db *gorm.DB, w io.Writer, merge *service.MergeService,
	actor int64, note, class string, limit int, run bool) error {
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

// collectGroups returns the requested class(es) of dedup groups.
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
