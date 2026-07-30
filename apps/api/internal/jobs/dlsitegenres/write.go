package dlsitegenres

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// writer applies planned tag rows with the ON CONFLICT idempotency backstop
// (serial, plain ints) — the worktags writer shape, FILL semantics (DO NOTHING,
// deliberately not step 62's refresh upsert: genre sets are quasi-static).
//
// W1-pre bridge nativization (refs/proj/140 §2.6): claimed and bodyless are peers
// now. The step-88 write-time XOR guard is gone together with the read-time bridge
// it protected — a claimed work's DLsite genres were never bridged from the wiki
// in the first place, and the tags facet's per-source lanes make this importer
// their single persistent writer for EVERY galgame work.
type writer struct {
	db    *gorm.DB
	stats *Stats
	// touched collects works whose genre tag set actually grew, so the run bumps
	// their catalog_work.updated_at once at the end and the public changes feed
	// learns the work is worth re-pulling. Conflicts, no-ops and dry-runs
	// contribute nothing, so a second --apply moves no watermark.
	touched []int64
}

// touch bumps updated_at on every work this run effectively wrote for.
func (w *writer) touch(ctx context.Context) error {
	return repository.TouchWorks(ctx, w.db, w.touched)
}

// plannedRow is one decided catalog_work_tag write. No Count field — DLsite
// genres carry no votes; every row lands count=0.
type plannedRow struct {
	WorkID   int64
	SourceID int16
	Name     string
}

// write inserts the row (apply only).
func (w *writer) write(ctx context.Context, p plannedRow, apply bool) {
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
	w.touched = append(w.touched, p.WorkID)
}
