package wikirescue

import (
	"context"
	"fmt"
	"time"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// wikiEngine is one galgame_engine row as the projection reads it.
type wikiEngine struct {
	ID          int64
	Name        string
	Description string
	Alias       []byte
}

// parkedEngineEdge records an engine edge whose work never got an anchor.
type parkedEngineEdge struct {
	GalgameID  int64  `json:"galgame_id"`
	EngineID   int64  `json:"engine_id"`
	EngineName string `json:"engine_name"`
}

// stepEngine migrates the engine facet (charter ruling 4): galgame_engine rows
// become catalog_engine rows verbatim, and the anchored engine_relation edges
// become catalog_work_engine rows attributed to galgame_wiki.
//
// This is the one facet with NO upstream fallback — VNDB publishes no engine
// data — so the wiki rows are the only copy in existence. Entities are matched
// by exact name, which is unique on both sides; a name that already exists in
// catalog_engine is REUSED (merged) rather than duplicated, and the count is
// reported.
func (r *Runner) stepEngine(ctx context.Context) (Stats, error) {
	st := Stats{Step: "a"}

	var engines []wikiEngine
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, name, coalesce(description, '') AS description, coalesce(alias, '[]'::jsonb) AS alias
		 FROM galgame_engine ORDER BY id`).Scan(&engines).Error; err != nil {
		return st, fmt.Errorf("read galgame_engine: %w", err)
	}
	st.Source = len(engines)

	// Existing catalog_engine rows keyed by name — the merge probe.
	existing, err := loadEngineIDs(ctx, r.catalog)
	if err != nil {
		return st, err
	}
	merged := 0
	toCreate := make([]model.CatalogEngine, 0, len(engines))
	for _, e := range engines {
		if _, ok := existing[e.Name]; ok {
			merged++
			continue
		}
		alias := e.Alias
		if len(alias) == 0 {
			alias = []byte("[]")
		}
		toCreate = append(toCreate, model.CatalogEngine{
			Name:        e.Name,
			Description: e.Description,
			Aliases:     alias,
		})
	}
	st.Planned = len(toCreate)
	st.Note = fmt.Sprintf("engines merged onto existing catalog_engine rows: %d", merged)

	if r.opts.Apply && len(toCreate) > 0 {
		res := r.catalog.WithContext(ctx).
			Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoNothing: true}).
			CreateInBatches(&toCreate, 500)
		if res.Error != nil {
			return st, fmt.Errorf("create catalog_engine: %w", res.Error)
		}
		st.Created = int(res.RowsAffected)
	}

	// Re-read so the edge projection sees both pre-existing and just-created
	// rows; on a dry run this is simply the pre-existing set.
	byName, err := loadEngineIDs(ctx, r.catalog)
	if err != nil {
		return st, err
	}
	engineTarget := make(map[int64]int64, len(engines))
	for _, e := range engines {
		if id, ok := byName[e.Name]; ok {
			engineTarget[e.ID] = id
		}
	}

	// ── edges ───────────────────────────────────────────────────────────────
	anchors, err := r.anchorMap(ctx)
	if err != nil {
		return st, err
	}
	type edge struct {
		GalgameID int64
		EngineID  int64
	}
	var edges []edge
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT galgame_id, engine_id FROM galgame_engine_relation ORDER BY galgame_id, engine_id`).
		Scan(&edges).Error; err != nil {
		return st, fmt.Errorf("read galgame_engine_relation: %w", err)
	}

	names := make(map[int64]string, len(engines))
	for _, e := range engines {
		names[e.ID] = e.Name
	}
	now := time.Now().UTC()
	rows := make([][]any, 0, len(edges))
	parked := make([]parkedEngineEdge, 0)
	for _, e := range edges {
		workID, ok := anchors[e.GalgameID]
		if !ok {
			parked = append(parked, parkedEngineEdge{GalgameID: e.GalgameID, EngineID: e.EngineID, EngineName: names[e.EngineID]})
			continue
		}
		target, ok := engineTarget[e.EngineID]
		if !ok {
			// Only reachable on a dry run, where the engine row does not exist
			// yet; counted as planned via the edge total, never parked.
			continue
		}
		rows = append(rows, []any{workID, target, r.wikiSrc, now, now})
	}
	st.Anchored = len(edges) - len(parked)
	st.Parked = len(parked)
	st.Planned += len(rows)

	if err := r.park("a-engine-edges", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}

	err = r.catalog.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		landed, err := insertReturning(tx, "catalog_work_engine",
			[]string{"work_id", "engine_id", "source_id", "created_at", "updated_at"}, "work_id", rows)
		if err != nil {
			return err
		}
		st.Written += len(landed)
		if len(landed) > 0 {
			if err := repository.TouchWorks(ctx, tx, landed); err != nil {
				return fmt.Errorf("touch works: %w", err)
			}
			st.Touched = distinctCount(landed)
		}
		return nil
	})
	return st, err
}

// loadEngineIDs maps catalog_engine.name → id.
func loadEngineIDs(ctx context.Context, db *gorm.DB) (map[string]int64, error) {
	type row struct {
		ID   int64
		Name string
	}
	var rows []row
	if err := db.WithContext(ctx).Raw(`SELECT id, name FROM catalog_engine`).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load catalog_engine: %w", err)
	}
	m := make(map[string]int64, len(rows))
	for _, x := range rows {
		m[x.Name] = x.ID
	}
	return m, nil
}

// distinctCount reports how many distinct work ids a touch covered.
func distinctCount(ids []int64) int {
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	return len(seen)
}
