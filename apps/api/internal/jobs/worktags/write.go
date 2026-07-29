package worktags

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// writer applies planned tag rows with the ON CONFLICT idempotency backstop
// (serial, plain ints). T2 (refs/proj/70 §3/§8, 88) retired the write-time XOR
// guard: the bgm folksonomy is a catalog-native SOURCE lane, so claimed and
// bodyless works materialize alike (the read face keeps the wiki bridge in its
// own lane).
type writer struct {
	db    *gorm.DB
	stats *Stats
	// touched collects the host works whose tag set actually grew, so the run
	// can bump their catalog_work.updated_at once at the end and let the public
	// changes feed see the facet write. Conflicts and dry-runs never land here,
	// which is what keeps a second --apply from re-emitting every work.
	touched []int64
}

// plannedRow is one decided catalog_work_tag write.
type plannedRow struct {
	WorkID   int64
	SourceID int16
	Name     string
	Count    int
}

// write (apply only) inserts the row, counting the ON CONFLICT no-op as a
// conflict.
func (w *writer) write(ctx context.Context, p plannedRow, apply bool) {
	if !apply {
		return
	}
	res := w.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "name"}, {Name: "source_id"}},
		DoNothing: true,
	}).Create(&model.CatalogWorkTag{
		WorkID: p.WorkID, Name: p.Name, Count: p.Count, SourceID: p.SourceID,
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
	w.touched = append(w.touched, p.WorkID)
}

// touch bumps updated_at on every work this run actually wrote a tag for.
func (w *writer) touch(ctx context.Context) error {
	return repository.TouchWorks(ctx, w.db, w.touched)
}
