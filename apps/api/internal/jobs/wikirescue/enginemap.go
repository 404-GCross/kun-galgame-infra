package wikirescue

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"api/internal/platform/catalog/model"
)

// matchedByEid is the provenance rule stamped on every engine mapping row.
const matchedByEid = "wiki:eid"

// parkedEngine records a wiki engine with no catalog_engine counterpart.
type parkedEngine struct {
	EngineID int64  `json:"engine_id"`
	Name     string `json:"name"`
}

// stepEngineMap lands the engine id → catalog_engine address map (A2-0,
// refs/proj/127).
//
// Step A moved the engine ROWS across but left no id bridge behind: catalog_engine
// has no wiki-id column, so once galgame_engine is dropped nothing can turn an
// eid back into an engine. The join medium is therefore the name, and it has to
// use EXACTLY step A's transform to stay lossless — step A matched on the raw
// galgame_engine.name with no trim and no folding (see stepEngine's
// loadEngineIDs / existing[e.Name] lookup), so this step matches on the raw name
// too. Name is UNIQUE on both sides, which makes the match 1:1; an unmatched
// engine is parked, never guessed at.
//
// EXACT link_kind and no touch, for the same reasons as step J: an eid is the
// engine's first-party wiki identity, and the changes feed carries works only.
func (r *Runner) stepEngineMap(ctx context.Context) (Stats, error) {
	st := Stats{Step: "k"}

	var existing int64
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT count(*) FROM catalog_external_ref WHERE entity_type = ? AND source_id = ?`,
		model.EntityTypeEngine, r.wikiSrc).Scan(&existing).Error; err != nil {
		return st, fmt.Errorf("probe existing eid refs: %w", err)
	}

	type wikiRow struct {
		ID   int64
		Name string
	}
	var engines []wikiRow
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, name FROM galgame_engine ORDER BY id`).Scan(&engines).Error; err != nil {
		return st, fmt.Errorf("read galgame_engine: %w", err)
	}
	st.Source = len(engines)

	byName, err := loadEngineIDs(ctx, r.catalog)
	if err != nil {
		return st, err
	}

	now := time.Now().UTC()
	rows := make([][]any, 0, len(engines))
	parked := make([]parkedEngine, 0)
	for _, e := range engines {
		target, ok := byName[e.Name]
		if !ok {
			parked = append(parked, parkedEngine{EngineID: e.ID, Name: e.Name})
			continue
		}
		rows = append(rows, []any{
			model.EntityTypeEngine, target, r.wikiSrc, strconv.FormatInt(e.ID, 10),
			model.LinkKindExact, matchedByEid, now,
		})
	}
	st.Anchored = len(rows)
	st.Parked = len(parked)
	st.Planned = len(rows)
	st.Note = fmt.Sprintf("pre-existing entity_type=engine source=galgame_wiki refs: %d; catalog_engine rows: %d; unmatched wiki engines: %d",
		existing, len(byName), len(parked))

	if err := r.park("k-engines-unmatched", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}

	landed, err := insertReturning(r.catalog.WithContext(ctx), "catalog_external_ref",
		[]string{"entity_type", "entity_id", "source_id", "external_id", "link_kind", "matched_by", "created_at"},
		"entity_id", rows)
	if err != nil {
		return st, err
	}
	st.Written = len(landed)
	return st, nil
}
