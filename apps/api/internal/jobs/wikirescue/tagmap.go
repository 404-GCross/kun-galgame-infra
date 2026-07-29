package wikirescue

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/catalog/model"
)

// matchedByTid is the provenance rule stamped on every tag mapping row.
const matchedByTid = "wiki:tid"

// sourceKeyVNDB is the catalog_source key the canonical tag layer files the
// WIKI tag vocabulary under. galgame_tag is the wiki's localized rendering of
// the VNDB tag list, so tagcanon (internal/jobs/tagcanon, vocab.go
// loadVndbVocab) registers every galgame_tag.name in catalog_tag_source_map as
// source `vndb` — NOT as `galgame_wiki`. Resolved by key at run time, never
// hardcoded, exactly as tagcanon's own resolveSources does: a rehearsal or prod
// database seeded in a different order still works.
const sourceKeyVNDB = "vndb"

// parkedTag is one wiki tag with no canonical counterpart — the P2 410 list.
type parkedTag struct {
	TagID int64  `json:"tag_id"`
	Name  string `json:"name"`
}

// tagNameCollision is a trimmed name carried by more than one galgame_tag row.
type tagNameCollision struct {
	Name   string  `json:"name"`
	TagIDs []int64 `json:"tag_ids"`
}

// stepTagMap lands the tag id → catalog_tag address map (A2-0, refs/proj/127).
//
// Unlike engines, the wiki tag vocabulary was never copied into catalog: the
// canonical layer (doc 70/74, internal/jobs/tagcanon) CONVERGED it — many wiki
// names fold onto one catalog_tag, and only names that formed a cross-source
// group or cleared the single-source usage gate got a row at all. So the map
// medium is catalog_tag_source_map, and its key is (source_id, source_name)
// where source_id is `vndb` and source_name is the galgame_tag name exactly as
// tagcanon stored it: strings.TrimSpace of the original, NO normalization —
// normalize() is only tagcanon's grouping key, never its storage form
// (vocab.go loadVndbVocab). Matching on anything else would silently mis-resolve.
//
// About half the vocabulary has no canonical row and cannot be resolved. Those
// are PARKED in full — that artifact IS a deliverable (the A2 P2 410 list), not
// a failure report.
//
// EXACT link_kind and no touch, for the same reasons as steps J/K.
func (r *Runner) stepTagMap(ctx context.Context) (Stats, error) {
	st := Stats{Step: "l"}

	vndbSrc, err := resolveSourceID(r.catalog, sourceKeyVNDB)
	if err != nil {
		return st, err
	}

	var existing int64
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT count(*) FROM catalog_external_ref WHERE entity_type = ? AND source_id = ?`,
		model.EntityTypeTag, r.wikiSrc).Scan(&existing).Error; err != nil {
		return st, fmt.Errorf("probe existing tid refs: %w", err)
	}

	type wikiRow struct {
		ID   int64
		Name string
	}
	var tags []wikiRow
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, name FROM galgame_tag ORDER BY id`).Scan(&tags).Error; err != nil {
		return st, fmt.Errorf("read galgame_tag: %w", err)
	}
	st.Source = len(tags)

	canonical, err := loadTagSourceMap(ctx, r, vndbSrc)
	if err != nil {
		return st, err
	}

	now := time.Now().UTC()
	rows := make([][]any, 0, len(tags))
	parked := make([]parkedTag, 0)
	byName := map[string][]int64{}
	for _, t := range tags {
		name := strings.TrimSpace(t.Name)
		byName[name] = append(byName[name], t.ID)
		tagID, ok := canonical[name]
		if !ok {
			parked = append(parked, parkedTag{TagID: t.ID, Name: t.Name})
			continue
		}
		rows = append(rows, []any{
			model.EntityTypeTag, tagID, r.wikiSrc, strconv.FormatInt(t.ID, 10),
			model.LinkKindExact, matchedByTid, now,
		})
	}
	// Several DIFFERENTLY-named wiki tags landing on one catalog_tag is the
	// canonical layer working as designed (each keeps its own external_id, so the
	// unique index is untouched). Several wiki rows sharing ONE name is a wiki
	// data anomaly instead: it is recorded and reported, but still written — each
	// row has its own tid, and dropping it would leave that tid unresolvable,
	// which is the exact failure this wave exists to prevent.
	collisions := make([]tagNameCollision, 0)
	for name, ids := range byName {
		if len(ids) > 1 {
			collisions = append(collisions, tagNameCollision{Name: name, TagIDs: ids})
		}
	}
	sort.Slice(collisions, func(i, j int) bool { return collisions[i].Name < collisions[j].Name })

	st.Anchored = len(rows)
	st.Parked = len(parked)
	st.Planned = len(rows)
	st.Note = fmt.Sprintf("pre-existing entity_type=tag source=galgame_wiki refs: %d; canonical vndb map rows: %d; unresolvable wiki tags: %d; duplicate wiki tag names: %d",
		existing, len(canonical), len(parked), len(collisions))

	if err := r.park("l-tags-unmapped", parked); err != nil {
		return st, err
	}
	if err := r.park("l-tag-name-collisions", collisions); err != nil {
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

// loadTagSourceMap reads the canonical layer's wiki-side address book:
// source_name → catalog_tag id for one source. The (source_id, source_name)
// composite PK makes the result 1:1 by construction.
func loadTagSourceMap(ctx context.Context, r *Runner, sourceID int16) (map[string]int64, error) {
	var rows []struct {
		SourceName string `gorm:"column:source_name"`
		TagID      int64  `gorm:"column:tag_id"`
	}
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT source_name, tag_id FROM catalog_tag_source_map WHERE source_id = ?`, sourceID).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load catalog_tag_source_map: %w", err)
	}
	m := make(map[string]int64, len(rows))
	for _, x := range rows {
		m[x.SourceName] = x.TagID
	}
	return m, nil
}
