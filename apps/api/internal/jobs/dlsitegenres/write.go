package dlsitegenres

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// writer applies planned tag rows with the write-time XOR guard and the
// ON CONFLICT idempotency backstop (serial, plain ints) — the worktags writer
// shape, FILL semantics (DO NOTHING, deliberately not step 62's refresh
// upsert: genre sets are quasi-static).
type writer struct {
	db    *gorm.DB
	stats *Stats
}

// plannedRow is one decided catalog_work_tag write, carrying the work's site
// so the guard can re-assert bodylessness at the last moment. No Count field —
// DLsite genres carry no votes; every row lands count=0.
type plannedRow struct {
	WorkID   int64
	Site     *string
	SourceID int16
	Name     string
}

// write enforces the XOR guard, then (apply only) inserts the row.
func (w *writer) write(ctx context.Context, p plannedRow, apply bool) {
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
		slog.Warn("write tag", "work", p.WorkID, "name", p.Name, "source", p.SourceID, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 { // row already there (second pass / concurrent writer)
		w.stats.Conflict++
		return
	}
	w.stats.Written++
}

// isBodyless reports whether a catalog_work is bodyless (site NULL or ”) —
// the §8.D claim key, same shape as worktags/workratings.
func isBodyless(site *string) bool { return site == nil || *site == "" }
