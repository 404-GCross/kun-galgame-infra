package main

import (
	"fmt"
	"sort"
	"time"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Join constants — the ONLY legal path from a DLsite counter to a wiki galgame.
// Unlike the eg-scores sibling the source/medium ids are resolved from the
// registry BY KEY at runtime (the bgmsummaries/workratings discipline), so a
// database with different seeds still works; only the structural enums and the
// tenant key are pinned here.
const (
	entityTypeRelease int16  = 6              // catalog/model.EntityTypeRelease — workno anchors are SKU-natured (doc 17 R3)
	linkKindExact     int16  = 0              // catalog/model.LinkKindExact — identity assertion only
	wikiSite          string = "galgame_wiki" // catalog_work.site tenant key (NOT the medium key "galgame")
	sourceKeyDlsite   string = "dlsite"       // catalog_source registry key
	mediumKeyGalgame  string = "galgame"      // catalog_medium registry key — the game-domain line (ASMR out by ruling)
)

// Options controls an enrich-dlsite-meta run.
type Options struct {
	Apply bool // false = dry run (resolve + count, no writes)
	Limit int  // cap works processed (0 = all); for a quick sampled trial
}

// Stats is the run summary. Identity: anchors - multiAnchor = matched +
// missingInMirror; matched = written + unchanged + skippedNoGalgame. In a dry
// run every would-be upsert counts as Written (the plan); only an apply can
// split the change-detected Written / Unchanged outcome.
type Stats struct {
	Anchors          int `json:"anchors"`            // DLsite exact release anchors resolving to a claimed galgame_wiki galgame-medium work (raw join rows)
	MultiAnchor      int `json:"multi_anchor"`       // extra anchors collapsed because a work carried >1 DLsite release anchor
	Matched          int `json:"matched"`            // distinct target galgames whose workno exists in the DLsite mirror
	Written          int `json:"written"`            // rows inserted or value-updated (dry run: planned upserts)
	Unchanged        int `json:"unchanged"`          // apply-only: change-detected no-ops (values already current)
	MissingInMirror  int `json:"missing_in_mirror"`  // chosen workno absent from the mirror
	SkippedNoGalgame int `json:"skipped_no_galgame"` // target galgame absent from the local wiki (snapshot drift / FK guard)
}

// dlData is one DLsite mirror works row, extracted from info_json (surveyed
// 2026-07-21: info_json carries all five values on every mirror row —
// product_json has the star but none of the counters). Every field is nullable:
// `->>` yields SQL NULL for an absent key AND for a JSON null, which is exactly
// the "DLsite does not publish this counter" semantics the meta table stores.
type dlData struct {
	rateStar  *float64 // info_json->rate_average_2dp — the native 0-5 star average (see the model doc)
	rateCount *int
	dlCount   *int64
	wishlist  *int64
	reviews   *int
}

// Run enriches galgame_dlsite_meta from the DLsite mirror via the catalog
// exact release anchor. catalogDB reads catalog_external_ref + catalog_release
// + catalog_work (+ the registry); dlsiteDB reads the mirror `works`; wikiDB
// is the write side (existence guard + upsert). In production these are three
// distinct databases; the integration test points all three at one
// schema-isolated fixture DB.
func Run(wikiDB, catalogDB, dlsiteDB *gorm.DB, opts Options) (*Stats, error) {
	stats := &Stats{}

	srcID, mediumID, err := resolveRegistry(catalogDB)
	if err != nil {
		return nil, err
	}

	// 1. Anchors: DLsite exact RELEASE refs whose work is a claimed
	// galgame_wiki galgame-medium row. external_id is the workno;
	// product_work_id is the wiki galgame.id.
	type anchorRow struct {
		Workno    string `gorm:"column:workno"`
		GalgameID int64  `gorm:"column:galgame_id"`
	}
	q := catalogDB.Table("catalog_external_ref AS er").
		Select("er.external_id AS workno, w.product_work_id AS galgame_id").
		Joins("JOIN catalog_release AS rel ON rel.id = er.entity_id AND rel.deleted_at IS NULL").
		Joins("JOIN catalog_work AS w ON w.id = rel.work_id").
		Where("er.source_id = ? AND er.entity_type = ? AND er.link_kind = ?", srcID, entityTypeRelease, linkKindExact).
		Where("w.site = ? AND w.product_work_id IS NOT NULL AND w.deleted_at IS NULL", wikiSite).
		Where("w.medium_id = ?", mediumID).
		Order("w.product_work_id, er.external_id")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	var anchors []anchorRow
	if err := q.Scan(&anchors).Error; err != nil {
		return nil, fmt.Errorf("load DLsite exact release anchors: %w", err)
	}
	stats.Anchors = len(anchors)

	// Group worknos by target galgame (a work MAY carry several DLsite SKUs;
	// zero such galgame works exist live, but the collapse stays deterministic).
	byGalgame := map[int64][]string{}
	for _, a := range anchors {
		byGalgame[a.GalgameID] = append(byGalgame[a.GalgameID], a.Workno)
	}

	// 2. Mirror rows for every referenced workno.
	worknoSet := map[string]bool{}
	for _, ws := range byGalgame {
		for _, w := range ws {
			worknoSet[w] = true
		}
	}
	mirror, err := loadMirror(dlsiteDB, keysOf(worknoSet))
	if err != nil {
		return nil, fmt.Errorf("load DLsite mirror works: %w", err)
	}

	// 3. Which target galgames actually exist in the local wiki (EXISTS guard:
	// the FK protection + resilience to any snapshot id-frontier drift).
	galgameIDs := make([]int64, 0, len(byGalgame))
	for gid := range byGalgame {
		galgameIDs = append(galgameIDs, gid)
	}
	existing, err := loadExistingGalgames(wikiDB, galgameIDs)
	if err != nil {
		return nil, fmt.Errorf("load existing galgames: %w", err)
	}

	// 4. One decision per distinct target galgame (deterministic order).
	sort.Slice(galgameIDs, func(i, j int) bool { return galgameIDs[i] < galgameIDs[j] })
	now := time.Now()
	for _, gid := range galgameIDs {
		worknos := byGalgame[gid]
		chosen := pickBest(worknos, mirror)
		if len(worknos) > 1 {
			stats.MultiAnchor += len(worknos) - 1
		}
		dl, ok := mirror[chosen]
		if !ok {
			stats.MissingInMirror++
			continue
		}
		stats.Matched++
		if !existing[gid] {
			stats.SkippedNoGalgame++
			continue
		}
		if !opts.Apply {
			stats.Written++ // dry run: the planned upsert
			continue
		}
		effective, err := upsertDlsiteMeta(wikiDB, model.GalgameDlsiteMeta{
			GalgameID:       int(gid),
			Workno:          chosen,
			RateAverageStar: dl.rateStar,
			RateCount:       dl.rateCount,
			DlCount:         dl.dlCount,
			WishlistCount:   dl.wishlist,
			ReviewCount:     dl.reviews,
			SyncedAt:        now,
		})
		if err != nil {
			return nil, fmt.Errorf("upsert dlsite meta galgame %d: %w", gid, err)
		}
		if effective {
			stats.Written++
		} else {
			stats.Unchanged++
		}
	}
	return stats, nil
}

// resolveRegistry looks up the dlsite source and galgame medium ids by key.
func resolveRegistry(catalogDB *gorm.DB) (srcID, mediumID int16, err error) {
	if err = catalogDB.Raw(`SELECT id FROM catalog_source WHERE key = ?`, sourceKeyDlsite).Scan(&srcID).Error; err != nil {
		return 0, 0, fmt.Errorf("resolve dlsite source: %w", err)
	}
	if err = catalogDB.Raw(`SELECT id FROM catalog_medium WHERE key = ?`, mediumKeyGalgame).Scan(&mediumID).Error; err != nil {
		return 0, 0, fmt.Errorf("resolve galgame medium: %w", err)
	}
	if srcID == 0 || mediumID == 0 {
		return 0, 0, fmt.Errorf("registry not seeded (dlsite source=%d, galgame medium=%d)", srcID, mediumID)
	}
	return srcID, mediumID, nil
}

// pickBest chooses the representative workno for a work with several DLsite
// release anchors: most-rated wins (biggest rate_count = the SKU users actually
// buy), tie-break higher dl_count, then the lexicographically smaller workno.
// Worknos absent from the mirror sort last so a present anchor always wins.
func pickBest(worknos []string, mirror map[string]dlData) string {
	best := worknos[0]
	for _, w := range worknos[1:] {
		if worse(best, w, mirror) {
			best = w
		}
	}
	return best
}

// worse reports whether a is a WORSE representative than b.
func worse(a, b string, mirror map[string]dlData) bool {
	da, oka := mirror[a]
	db, okb := mirror[b]
	if oka != okb {
		return !oka // present beats absent
	}
	if !oka {
		return a > b
	}
	if ra, rb := derefIntOr(da.rateCount, -1), derefIntOr(db.rateCount, -1); ra != rb {
		return ra < rb
	}
	if la, lb := derefInt64Or(da.dlCount, -1), derefInt64Or(db.dlCount, -1); la != lb {
		return la < lb
	}
	return a > b
}

func derefIntOr(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}

func derefInt64Or(p *int64, fallback int64) int64 {
	if p == nil {
		return fallback
	}
	return *p
}

// loadMirror batch-loads the DLsite mirror rows, extracting the five values
// from info_json. Negative counters (one corrupt row observed live) and a
// vote-less star (none observed — star ⟺ rate_count>=5 in the survey) are
// normalized to NULL: never store a fake or impossible value.
func loadMirror(dlsiteDB *gorm.DB, worknos []string) (map[string]dlData, error) {
	out := map[string]dlData{}
	type row struct {
		Workno    string   `gorm:"column:workno"`
		RateStar  *float64 `gorm:"column:rate_star"`
		RateCount *int     `gorm:"column:rate_count"`
		DlCount   *int64   `gorm:"column:dl_count"`
		Wishlist  *int64   `gorm:"column:wishlist_count"`
		Reviews   *int     `gorm:"column:review_count"`
	}
	for start := 0; start < len(worknos); start += 1000 {
		end := min(start+1000, len(worknos))
		var batch []row
		if err := dlsiteDB.Table("works").
			Select(`workno,
				(info_json->>'rate_average_2dp')::float8 AS rate_star,
				(info_json->>'rate_count')::int          AS rate_count,
				(info_json->>'dl_count')::bigint         AS dl_count,
				(info_json->>'wishlist_count')::bigint   AS wishlist_count,
				(info_json->>'review_count')::int        AS review_count`).
			Where("workno IN ?", worknos[start:end]).Scan(&batch).Error; err != nil {
			return nil, err
		}
		for _, r := range batch {
			d := dlData{
				rateStar:  r.RateStar,
				rateCount: dropNegInt(r.RateCount),
				dlCount:   dropNegInt64(r.DlCount),
				wishlist:  dropNegInt64(r.Wishlist),
				reviews:   dropNegInt(r.Reviews),
			}
			if d.rateCount == nil || *d.rateCount <= 0 {
				d.rateStar, d.rateCount = nil, nil // the pair lives and dies together
			}
			out[r.Workno] = d
		}
	}
	return out, nil
}

func dropNegInt(p *int) *int {
	if p != nil && *p < 0 {
		return nil
	}
	return p
}

func dropNegInt64(p *int64) *int64 {
	if p != nil && *p < 0 {
		return nil
	}
	return p
}

// loadExistingGalgames batch-loads which of the target galgame ids exist.
func loadExistingGalgames(wikiDB *gorm.DB, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	for start := 0; start < len(ids); start += 1000 {
		end := min(start+1000, len(ids))
		var found []int64
		if err := wikiDB.Table("galgame").Where("id IN ?", ids[start:end]).
			Pluck("id", &found).Error; err != nil {
			return nil, err
		}
		for _, id := range found {
			out[id] = true
		}
	}
	return out, nil
}

// upsertDlsiteMeta writes one row with the CHANGE-DETECTED upsert: the DO
// UPDATE fires only when a value column actually differs (row-wise IS DISTINCT
// FROM handles the NULLs), so synced_at advances only on real change and
// RowsAffected cleanly separates effective writes (true) from no-ops (false).
func upsertDlsiteMeta(db *gorm.DB, row model.GalgameDlsiteMeta) (bool, error) {
	res := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "galgame_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"workno", "rate_average_star", "rate_count", "dl_count", "wishlist_count", "review_count", "synced_at",
		}),
		Where: clause.Where{Exprs: []clause.Expression{gorm.Expr(
			`(galgame_dlsite_meta.workno, galgame_dlsite_meta.rate_average_star, galgame_dlsite_meta.rate_count,
			  galgame_dlsite_meta.dl_count, galgame_dlsite_meta.wishlist_count, galgame_dlsite_meta.review_count)
			 IS DISTINCT FROM
			 (excluded.workno, excluded.rate_average_star, excluded.rate_count,
			  excluded.dl_count, excluded.wishlist_count, excluded.review_count)`)}},
	}).Create(&row)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
