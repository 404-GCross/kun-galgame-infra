package wikirescue

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// matchedByGid is the provenance rule stamped on every gid mapping row.
const matchedByGid = "wiki:gid"

// stepGidMap lands the gid → catalog_work address map (charter "默认结构件").
//
// This is the piece that keeps the wiki's URL space resolvable after the table
// family is dropped: every old /galgame/<id> link, every downstream row still
// holding a gid, and the Sunset redirects all need to turn a gid into a work.
// Once galgame is gone the pointer column goes with it, so the mapping has to
// live in catalog_external_ref, which is where every other id space already
// lives.
//
// link_kind is EXACT here, unlike the related-grade link step: a gid IS the
// work's first-party identity in the wiki id space, not a reference to some
// other page. That makes the rows subject to the partial unique index on
// (source_id, external_id, entity_type) WHERE link_kind=0 — safe by
// construction, since galgame.id is a primary key and the anchor map is 1:1.
//
// Runs LAST: step H mints and re-anchors works, and those must appear in the
// map too.
func (r *Runner) stepGidMap(ctx context.Context) (Stats, error) {
	st := Stats{Step: "i"}

	var existing int64
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT count(*) FROM catalog_external_ref WHERE entity_type = ? AND source_id = ?`,
		model.EntityTypeWork, r.wikiSrc).Scan(&existing).Error; err != nil {
		return st, fmt.Errorf("probe existing gid refs: %w", err)
	}

	anchors, err := r.anchorMap(ctx)
	if err != nil {
		return st, err
	}
	st.Source = len(anchors)
	st.Anchored = len(anchors)

	now := time.Now().UTC()
	rows := make([][]any, 0, len(anchors))
	for gid, workID := range anchors {
		rows = append(rows, []any{
			model.EntityTypeWork, workID, r.wikiSrc, strconv.FormatInt(gid, 10),
			model.LinkKindExact, matchedByGid, now,
		})
	}
	st.Planned = len(rows)
	st.Note = fmt.Sprintf("pre-existing entity_type=work source=galgame_wiki refs: %d", existing)

	if !r.opts.Apply {
		return st, nil
	}

	// A one-shot backfill of this size is a deliberate changes-feed event: every
	// mapped work gains a public identity ref, so every mapped work is touched.
	err = r.catalog.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		landed, err := insertReturning(tx, "catalog_external_ref",
			[]string{"entity_type", "entity_id", "source_id", "external_id", "link_kind", "matched_by", "created_at"},
			"entity_id", rows)
		if err != nil {
			return err
		}
		st.Written = len(landed)
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
