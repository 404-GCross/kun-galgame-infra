package bgmworkmeta

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// writer applies planned rows with a PER-FIELD claim policy (T2, refs/proj/70
// §3/§8, 88): Field A (meta tags) has NO guard — meta_tags is a catalog-native
// SOURCE lane, so claimed and bodyless works materialize alike; it is a FILL:
// ON CONFLICT (work_id, name, source_id) DO NOTHING — a same-name folksonomy row
// keeps its votes, a second pass counts pure conflicts. Field B (favorite
// shelves) KEEPS the write-time XOR guard: the popularity facet's XOR is not in
// the T2 tags decision domain (a future T2b decides it), so claimed favorites
// are still refused. It is the step-62 CHANGE-DETECTED upsert: ON CONFLICT DO
// UPDATE fires only when the value actually differs, so RowsAffected cleanly
// separates effective writes from refresh no-ops.
type writer struct {
	db    *gorm.DB
	stats *Stats
}

// tagRow is one decided catalog_work_tag write (Count is always 0 — the
// moderated-meta-tag marker).
type tagRow struct {
	WorkID   int64
	SourceID int16
	Name     string
}

// writeTag (apply only) inserts the Count=0 row. T2: no claim guard — meta tags
// materialize for claimed and bodyless works alike.
func (w *writer) writeTag(ctx context.Context, p tagRow, apply bool) {
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

// writeFavorite enforces the per-field XOR guard (Field B still refuses claimed
// — popularity's XOR is not in the T2 tags domain), then (apply only) upserts
// the shelf row — the workratings writePopularity pattern verbatim.
func (w *writer) writeFavorite(ctx context.Context, p favRow, apply bool) {
	if !isBodyless(p.Site) { // XOR guard (§8.D) — claimed favorites still refused (Field B)
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
