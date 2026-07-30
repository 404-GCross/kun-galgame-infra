// works_facets.go — the A2-1d additions to the reindexer: the works index's
// product-search axes (tag / label / engine / series ids, the earliest release
// ordinal, olang, claimed, updated) and the new canonical-tag index.
//
// Every loader here follows the file's established shape: ONE query for the
// whole population into a map, scoped by a join to the very population the
// works index carries (LIVE, non-deleted, galgame medium), so the per-batch
// loop stays free of round trips.
package main

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/platform/catalog/model"
	catalogSearch "api/internal/platform/catalog/search"

	"gorm.io/gorm"
)

// worksPopulationJoin is the predicate every works-scoped loader shares — the
// same set reindexWorks itself pages over, so no loader can drift from the
// index population.
const worksPopulationJoin = `JOIN catalog_work w ON w.id = %s AND w.deleted_at IS NULL
	AND w.medium_id = (SELECT id FROM catalog_medium WHERE key = 'galgame') AND w.status = 0`

// loadWorkEdgeIDs returns work id → the distinct ids of one edge facet, ordered
// so the document is byte-stable across reruns (an unstable array would make
// every reindex look like a change to Meilisearch).
//
// edge is a fixed internal SQL fragment producing (work_id, key_id) — never
// caller input.
func loadWorkEdgeIDs(db *gorm.DB, edge string) (map[int64][]int64, error) {
	var rows []struct {
		WorkID int64 `gorm:"column:work_id"`
		KeyID  int64 `gorm:"column:key_id"`
	}
	q := fmt.Sprintf(`SELECT DISTINCT e.work_id, e.key_id FROM %s `+worksPopulationJoin+
		` ORDER BY e.work_id, e.key_id`, edge, "e.work_id")
	if err := db.Raw(q).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := map[int64][]int64{}
	for _, r := range rows {
		m[r.WorkID] = append(m[r.WorkID], r.KeyID)
	}
	return m, nil
}

// The four edge fragments. tag ids resolve THROUGH catalog_tag_source_map so the
// index speaks canonical tag ids — the same id space the public tag_id filter
// and GET /v1/catalog/tags use. The others are the edge tables verbatim.
//
// These mirror the read face's own edge definitions (service/public_taxonomy.go
// labelWorkEdge / tagWorkEdge / engineWorkEdge and the works list's series
// EXISTS clause); a filter that matched here but not there would be the worst
// kind of drift, so they are written to be diffable against each other.
const (
	reindexTagEdge = `(SELECT m.tag_id AS key_id, wt.work_id
		FROM catalog_work_tag wt
		JOIN catalog_tag_source_map m ON m.source_id = wt.source_id AND m.source_name = wt.name) e`
	reindexLabelEdge  = `(SELECT label_id AS key_id, work_id FROM catalog_work_label) e`
	reindexEngineEdge = `(SELECT engine_id AS key_id, work_id FROM catalog_work_engine) e`
	reindexSeriesEdge = `(SELECT series_id AS key_id, work_id FROM catalog_series_member) e`
)

// loadEarliestReleaseOrd returns work id → the COMPOSED ORDINAL
// (y*10000 + m*100 + d, unknown month/day = 0) of its earliest year-carrying,
// non-deleted release. Works with no dated release are simply absent, which is
// what leaves released_ord off the document (see EntityDoc.ReleasedOrd).
//
// This is the same expression the works list projects as release_date and the
// calendar buckets on — literally releaseOrd() in service/public_calendar.go —
// so a work's search filter, its printed date and its calendar month can never
// disagree.
func loadEarliestReleaseOrd(db *gorm.DB) (map[int64]int64, error) {
	var rows []struct {
		WorkID int64 `gorm:"column:work_id"`
		Ord    int64 `gorm:"column:ord"`
	}
	q := fmt.Sprintf(`SELECT r.work_id,
			min(r.released_y::int * 10000 + coalesce(r.released_m,0)::int * 100 + coalesce(r.released_d,0)::int) AS ord
		FROM catalog_release r `+worksPopulationJoin+`
		WHERE r.released_y IS NOT NULL AND r.deleted_at IS NULL
		GROUP BY r.work_id`, "r.work_id")
	if err := db.Raw(q).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(rows))
	for _, r := range rows {
		m[r.WorkID] = r.Ord
	}
	return m, nil
}

// worksFacets bundles everything the works document needs beyond the registry
// row itself, loaded once per reindex run.
type worksFacets struct {
	tagIDs      map[int64][]int64
	labelIDs    map[int64][]int64
	engineIDs   map[int64][]int64
	seriesIDs   map[int64][]int64
	releasedOrd map[int64]int64
}

func loadWorksFacets(db *gorm.DB) (worksFacets, error) {
	var f worksFacets
	var err error
	if f.tagIDs, err = loadWorkEdgeIDs(db, reindexTagEdge); err != nil {
		return f, fmt.Errorf("work tag ids: %w", err)
	}
	if f.labelIDs, err = loadWorkEdgeIDs(db, reindexLabelEdge); err != nil {
		return f, fmt.Errorf("work label ids: %w", err)
	}
	if f.engineIDs, err = loadWorkEdgeIDs(db, reindexEngineEdge); err != nil {
		return f, fmt.Errorf("work engine ids: %w", err)
	}
	if f.seriesIDs, err = loadWorkEdgeIDs(db, reindexSeriesEdge); err != nil {
		return f, fmt.Errorf("work series ids: %w", err)
	}
	if f.releasedOrd, err = loadEarliestReleaseOrd(db); err != nil {
		return f, fmt.Errorf("earliest release ordinal: %w", err)
	}
	return f, nil
}

// --- tags lane (A2-1d: the canonical tag vocabulary joins the entity search) --

// loadTagWorkCounts returns canonical tag id → the number of DISTINCT LIVE
// galgame works carrying any source tag mapped to it.
//
// This is a RANKING SIGNAL, not a wire count: BuildTagDoc log-damps it into the
// tags index's popularity tiebreaker and never publishes the number. It is
// therefore deliberately the WIDE population — nsfw included, and (unlike
// GET /v1/catalog/tags's work_count since wave 146) claim state included too:
// how much a tag is used across the whole registry is the honest ranking input,
// while the published count answers the narrower "what will I get if I click
// this".
func loadTagWorkCounts(db *gorm.DB) (map[int64]int, error) {
	var rows []struct {
		TagID int64 `gorm:"column:tag_id"`
		Cnt   int   `gorm:"column:cnt"`
	}
	q := fmt.Sprintf(`SELECT e.key_id AS tag_id, count(DISTINCT e.work_id) AS cnt
		FROM %s `+worksPopulationJoin+` GROUP BY e.key_id`, reindexTagEdge, "e.work_id")
	if err := db.Raw(q).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int, len(rows))
	for _, r := range rows {
		m[r.TagID] = r.Cnt
	}
	return m, nil
}

// reindexTags builds the catalog_tags index: every canonical tag, searchable by
// name, filterable by tier + kind, ranked by how many works carry it.
//
// catalog_tag has no lang column (the canonical name is whatever string the
// group converged on), so the language bucket is guessed the same way the works
// lane guesses a bare display_name — the CJK locale pinning needs a bucket, and
// a wrong guess between zh and ja costs tokenization nuance, never a miss.
func reindexTags(ctx context.Context, db *gorm.DB, idx *catalogSearch.Indexer, batch int) error {
	counts, err := loadTagWorkCounts(db)
	if err != nil {
		return err
	}
	// A2-0 registered wiki tag ids as entity_type=7 external refs, so a consumer
	// can resolve an old wiki tid straight off a search hit.
	srcs, keys, err := loadSources(db, model.EntityTypeTag)
	if err != nil {
		return err
	}
	processed, lastID := 0, int64(0)
	for {
		var rows []struct {
			ID   int64  `gorm:"column:id"`
			Name string `gorm:"column:name"`
			Tier int16  `gorm:"column:tier"`
			Kind int16  `gorm:"column:kind"`
		}
		if err := db.Raw(`SELECT id, name, tier, kind FROM catalog_tag WHERE id > ? ORDER BY id LIMIT ?`,
			lastID, batch).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		docs := make([]catalogSearch.EntityDoc, len(rows))
		for i, r := range rows {
			docs[i] = catalogSearch.BuildTagDoc(catalogSearch.TagDocInput{
				ID: r.ID, Name: r.Name, Tier: r.Tier, Kind: r.Kind,
				WorkCount: counts[r.ID], Sources: srcs[r.ID], SourceKeys: keys[r.ID],
			})
		}
		if err := idx.UpsertBatch(ctx, catalogSearch.IndexTags, docs); err != nil {
			return err
		}
		processed += len(rows)
		lastID = rows[len(rows)-1].ID
	}
	slog.Info("reindexed", "index", catalogSearch.IndexTags, "docs", processed)
	return nil
}
