package bgmworkmeta

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// writer applies planned rows with the write-time XOR guard. Field A (meta
// tags) is a FILL: ON CONFLICT (work_id, name, source_id) DO NOTHING — a
// same-name folksonomy row keeps its votes, a second pass counts pure
// conflicts. Field B (favorite shelves) is the step-62 CHANGE-DETECTED upsert:
// ON CONFLICT DO UPDATE fires only when the value actually differs, so
// RowsAffected cleanly separates effective writes from refresh no-ops.
type writer struct {
	db    *gorm.DB
	stats *Stats
}

// tagRow is one decided catalog_work_tag write (Count is always 0 — the
// moderated-meta-tag marker), carrying the work's site so the guard can
// re-assert bodylessness at the last moment.
type tagRow struct {
	WorkID   int64
	Site     *string
	SourceID int16
	Name     string
}

// writeTag enforces the XOR guard, then (apply only) inserts the Count=0 row.
func (w *writer) writeTag(ctx context.Context, p tagRow, apply bool) {
	if !isBodyless(p.Site) { // XOR guard (§8.D) — never materialise a claimed work
		w.stats.Refused++
		return
	}
	if !apply {
		return
	}
	res := w.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "name"}, {Name: "source_id"}},
		DoNothing: true,
	}).Create(&model.CatalogWorkTag{
		WorkID: p.WorkID, Name: p.Name, Count: 0, SourceID: p.SourceID,
	})
	if res.Error != nil {
		w.stats.Errors++
		slog.Warn("write meta tag", "work", p.WorkID, "name", p.Name, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 { // existing row (voted folksonomy or earlier meta) kept
		w.stats.MetaConflict++
		return
	}
	w.stats.MetaWritten++
}

// favRow is one decided catalog_work_popularity write.
type favRow struct {
	WorkID   int64
	Site     *string
	SourceID int16
	Metric   int16
	Value    int64
}

// writeFavorite enforces the XOR guard, then (apply only) upserts the shelf
// row — the workratings writePopularity pattern verbatim.
func (w *writer) writeFavorite(ctx context.Context, p favRow, apply bool) {
	if !isBodyless(p.Site) { // XOR guard (§8.D)
		w.stats.Refused++
		return
	}
	if !apply {
		return
	}
	res := w.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "source_id"}, {Name: "metric"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
		Where: clause.Where{Exprs: []clause.Expression{gorm.Expr(
			`catalog_work_popularity.value IS DISTINCT FROM excluded.value`)}},
	}).Create(&model.CatalogWorkPopularity{
		WorkID: p.WorkID, SourceID: p.SourceID, Metric: p.Metric, Value: p.Value,
	})
	if res.Error != nil {
		w.stats.Errors++
		slog.Warn("write favorite shelf", "work", p.WorkID, "metric", p.Metric, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 { // row already current (refresh no-op)
		w.stats.FavUnchanged++
		return
	}
	w.stats.FavWritten++
}

// isBodyless reports whether a catalog_work is bodyless (site NULL or ”) —
// the §8.D claim key, same shape as workratings.
func isBodyless(site *string) bool { return site == nil || *site == "" }
