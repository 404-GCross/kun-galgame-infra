package tagcanon

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// writer applies decided groups: one catalog_tag row + one catalog_tag_source_map
// row per member. Both writes are ON CONFLICT DO NOTHING, so a second full pass
// writes zero (counted as conflicts) — the idempotence guarantee (doc 74 §5).
type writer struct {
	db    *gorm.DB
	stats *Stats
}

// writeGroup ensures the canonical tag and its per-source map rows. In dry mode
// it decides everything but writes nothing. Errors are counted, never fatal —
// one bad group must not abort the run.
func (w *writer) writeGroup(ctx context.Context, g group, apply bool) {
	if !apply {
		return
	}
	tagID, ok := w.ensureTag(ctx, g)
	if !ok {
		return // error already counted
	}
	for _, m := range g.Members {
		res := w.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "source_id"}, {Name: "source_name"}},
			DoNothing: true,
		}).Create(&model.CatalogTagSourceMap{SourceID: m.SourceID, SourceName: m.Name, TagID: tagID})
		if res.Error != nil {
			w.stats.Errors++
			slog.Warn("write tag map", "source", m.SourceID, "name", m.Name, "err", res.Error)
			continue
		}
		if res.RowsAffected == 0 {
			w.stats.MapsConflict++
			continue
		}
		w.stats.MapsCreated++
	}
}

// ensureTag inserts the canonical tag (ON CONFLICT (name) DO NOTHING) and
// returns its id. On a fresh insert GORM fills the id via RETURNING; on a
// conflict (second pass) it fetches the existing id. tier/kind are set ONLY on
// insert — a re-run never rewrites them (zero-write idempotence; a later 70b
// tier retitling is a separate, deliberate write path).
func (w *writer) ensureTag(ctx context.Context, g group) (int64, bool) {
	tag := model.CatalogTag{Name: g.CanonicalName, Tier: g.Tier, Kind: g.Kind}
	res := w.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoNothing: true,
	}).Create(&tag)
	if res.Error != nil {
		w.stats.Errors++
		slog.Warn("write canonical tag", "name", g.CanonicalName, "err", res.Error)
		return 0, false
	}
	if res.RowsAffected == 1 {
		w.stats.TagsCreated++
		return tag.ID, true
	}
	// Conflict: the row already existed — fetch its id for the map writes.
	w.stats.TagsConflict++
	var id int64
	if err := w.db.WithContext(ctx).Raw(`SELECT id FROM catalog_tag WHERE name = ?`, g.CanonicalName).Scan(&id).Error; err != nil || id == 0 {
		w.stats.Errors++
		slog.Warn("resolve existing canonical tag id", "name", g.CanonicalName, "err", err)
		return 0, false
	}
	return id, true
}
