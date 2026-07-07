package catalogsync

import (
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
)

// runEG writes the erogamespace rosetta: where a wiki galgame's vndb_id maps
// to exactly ONE erogamespace row carrying that same vndb, attach a PROBABLE
// external_ref (source=erogamespace, external_id = EG game id) to the work.
//
// EG's vndb column is community-maintained (trust_tier=1) → probable, never
// exact, even when it double-confirms our own vndb_id (a bulk promotion to
// exact under a "double-source" rule is a NAMED future decision, out of scope
// here). Ambiguity is gated on BOTH sides: an EG vndb shared by several EG
// rows, or (structurally impossible under the wiki partial-unique index, but
// checked anyway) a vndb shared by several wiki games, is skipped and counted.
func (r *Reconciler) runEG() (EGStats, error) {
	var stats EGStats

	works, err := r.workIDMap()
	if err != nil {
		return stats, err
	}

	// EG side: group ids by normalized vndb; keep only the singletons.
	var egRows []struct {
		ID   int64  `gorm:"column:id"`
		VNDB string `gorm:"column:vndb"`
	}
	if err := r.eg.Raw(
		`SELECT id, vndb FROM games WHERE vndb IS NOT NULL AND vndb <> ''`,
	).Scan(&egRows).Error; err != nil {
		return stats, err
	}
	egByVNDB := make(map[string][]int64)
	for _, e := range egRows {
		k := normVNDB(e.VNDB)
		if k == "" {
			continue
		}
		egByVNDB[k] = append(egByVNDB[k], e.ID)
	}

	// Wiki side: detect (structurally-impossible) duplicate vndb_id defensively.
	wikiVNDBCount := make(map[string]int)
	for i := range r.games {
		if v := normVNDB(r.games[i].VNDBID); v != "" {
			wikiVNDBCount[v]++
		}
	}

	for i := range r.games {
		g := &r.games[i]
		k := normVNDB(g.VNDBID)
		if k == "" {
			continue
		}
		egIDs, ok := egByVNDB[k]
		if !ok {
			continue // no EG row for this vndb — silent absence, not a skip
		}
		if len(egIDs) > 1 || wikiVNDBCount[k] > 1 {
			stats.AmbiguousSkipped++
			continue
		}

		workID, claimed := works[g.ID]
		if !claimed {
			stats.Unclaimed++ // run --phase claim first
			continue
		}
		egExternalID := strconv.FormatInt(egIDs[0], 10)
		if r.rejected.Has(model.EntityTypeWork, workID, sourceEG, egExternalID) {
			stats.SkippedRejected++ // negative knowledge: never re-assert
			continue
		}
		stats.Matched++
		if r.dryRun {
			stats.RefsWritten++ // would write (clean-slate assumption)
			continue
		}
		written, err := repository.InsertRefIfAbsent(r.catalog, model.CatalogExternalRef{
			EntityType: model.EntityTypeWork,
			EntityID:   workID,
			SourceID:   sourceEG,
			ExternalID: egExternalID,
			LinkKind:   model.LinkKindProbable,
			MatchedBy:  ruleEGRosetta,
		})
		if err != nil {
			return stats, err
		}
		if written {
			stats.RefsWritten++
		} else {
			stats.Already++
		}
	}
	return stats, nil
}

// normVNDB folds a VNDB id (ASCII, "v<digits>") for cross-source equality.
func normVNDB(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}
