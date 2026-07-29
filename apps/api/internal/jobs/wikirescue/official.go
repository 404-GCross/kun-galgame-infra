package wikirescue

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/repository"
	"api/internal/platform/galgame/catalogsync"
)

// stepOfficial attaches the unprojected galgame_official tail to catalog labels
// by DELEGATING to catalogsync.OfficialRegistrar — the very lane that projected
// the other 23,365 officials. Re-implementing the mint here would fork a tested
// write path (mint + aliases + imported revision + collision candidates +
// bridge write-back + brand edges) for no gain.
//
// Two deliberate divergences from the W0 task sheet, both reported rather than
// papered over:
//
//   - The sheet asks for an exact-name auto-link onto an existing catalog_label
//     before minting. This lane refuses to: R8 makes name equality
//     CANDIDATE-grade only, so a collision becomes a pending
//     catalog_match_candidate for human review and the official still gets its
//     own label. Auto-equating on a name is the documented way to poison the
//     entity graph, so the sheet's shortcut is not taken here.
//   - The lane's edge phase re-projects EVERY mapped official's relations, not
//     just the tail's, so it backfills works claimed since the last run. That is
//     wider than the sheet's scope; the count is measured before the run and
//     reported.
//
// The lane does not bump catalog_work.updated_at when it writes edges, so this
// step supplies the touch the W0 discipline requires: the set of works about to
// gain an edge is computed BEFORE the run, and exactly that set is touched after.
func (r *Runner) stepOfficial(ctx context.Context) (Stats, error) {
	st := Stats{Step: "g"}

	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT count(*) FROM galgame_official WHERE catalog_label_id IS NULL OR catalog_label_id = 0`).
		Scan(&st.Source).Error; err != nil {
		return st, fmt.Errorf("count unmapped officials: %w", err)
	}

	// The works that will gain a Brand edge. Computed up front because the
	// registrar reports only totals, and TouchWorks must see exactly the
	// mutated set — never the whole candidate set.
	pending, err := r.pendingBrandEdgeWorks(ctx)
	if err != nil {
		return st, err
	}
	st.Planned = len(pending)

	reg := catalogsync.NewOfficialRegistrar(r.galgame.WithContext(ctx), r.catalog.WithContext(ctx), !r.opts.Apply, 0)
	ost, err := reg.Run()
	if err != nil {
		return st, fmt.Errorf("official registrar: %w", err)
	}
	st.Created = ost.Minted
	st.Linked = ost.Minted
	st.Note = fmt.Sprintf(
		"officials=%d minted=%d already=%d aliases=%d name_candidates=%d edges_written=%d edges_already=%d edges_skipped_unclaimed=%d",
		ost.Officials, ost.Minted, ost.Already, ost.AliasesWritten, ost.NameCandidates,
		ost.EdgesWritten, ost.EdgesAlready, ost.EdgesSkippedUnclaimed)

	if !r.opts.Apply {
		return st, nil
	}
	st.Written = ost.EdgesWritten
	if len(pending) > 0 {
		if err := repository.TouchWorks(ctx, r.catalog, pending); err != nil {
			return st, fmt.Errorf("touch works: %w", err)
		}
		st.Touched = distinctCount(pending)
	}
	return st, nil
}

// pendingBrandEdgeWorks lists the claimed works that a full official-relation
// re-projection would give a NEW brand edge to. Runs on the catalog pool: the
// wiki tables and catalog tables share a database in every current deployment,
// and the query needs both families in one plan.
func (r *Runner) pendingBrandEdgeWorks(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := r.catalog.WithContext(ctx).Raw(
		`WITH claimed AS (
		     SELECT product_work_id AS gid, id AS wid FROM catalog_work
		     WHERE site = 'galgame_wiki' AND product_work_id IS NOT NULL AND deleted_at IS NULL
		 ), proj AS (
		     SELECT DISTINCT c.wid AS work_id, o.catalog_label_id AS label_id
		     FROM galgame_official_relation r
		     JOIN claimed c ON c.gid = r.galgame_id
		     JOIN galgame_official o ON o.id = r.official_id
		     WHERE o.catalog_label_id IS NOT NULL AND o.catalog_label_id <> 0
		 )
		 SELECT DISTINCT p.work_id FROM proj p
		 LEFT JOIN catalog_work_label wl
		   ON wl.work_id = p.work_id AND wl.label_id = p.label_id AND wl.kind = ?
		 WHERE wl.work_id IS NULL`, 3).Scan(&ids).Error
	if err != nil {
		return nil, fmt.Errorf("compute pending brand edges: %w", err)
	}
	return ids, nil
}
