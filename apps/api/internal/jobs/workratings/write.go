package workratings

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// writer applies planned rating/popularity rows with the CHANGE-DETECTED upsert
// (step 62 upsert unification): ON CONFLICT DO UPDATE fires only when a value
// actually differs (row-wise IS DISTINCT FROM handles the NULLs), so RowsAffected
// cleanly separates effective writes (insert or value-update → *written) from
// refresh no-ops (→ *unchanged) and a re-run against unchanged staging performs
// zero updates.
//
// W1-pre bridge nativization (refs/proj/140 §2.6): claimed and bodyless are peers
// now. The step-88 write-time XOR guard is gone together with the read-time
// bridge it protected — the catalog read face no longer reads a claimed work's
// bangumi / dlsite / erogamespace scores out of the wiki meta tables, so this
// importer is their single persistent writer for EVERY galgame work.
type writer struct {
	db    *gorm.DB
	stats *Stats
	// touched collects works whose rating/popularity facet actually changed, so
	// one bump of catalog_work.updated_at at the end of the run puts them on the
	// public changes feed. A refresh no-op contributes nothing, so a re-run
	// against unchanged staging moves no watermark at all.
	touched []int64
}

// touch bumps updated_at on every work this run effectively wrote for.
func (w *writer) touch(ctx context.Context) error {
	return repository.TouchWorks(ctx, w.db, w.touched)
}

// plannedRow is one decided catalog_work_rating write.
type plannedRow struct {
	WorkID    int64
	SourceID  int16
	Score     float64
	VoteCount int
	Rank      *int
}

// write upserts the row (apply only). written / unchanged point at the owning
// lane's counters so all lanes share one path.
func (w *writer) write(ctx context.Context, p plannedRow, apply bool, written, unchanged *int) {
	if !apply {
		return
	}
	res := w.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "source_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"score", "vote_count", "rank", "updated_at"}),
		Where: clause.Where{Exprs: []clause.Expression{gorm.Expr(
			`(catalog_work_rating.score, catalog_work_rating.vote_count, catalog_work_rating.rank)
			 IS DISTINCT FROM (excluded.score, excluded.vote_count, excluded.rank)`)}},
	}).Create(&model.CatalogWorkRating{
		WorkID: p.WorkID, SourceID: p.SourceID, Score: p.Score, VoteCount: p.VoteCount, Rank: p.Rank,
	})
	if res.Error != nil {
		w.stats.Errors++
		slog.Warn("write rating", "work", p.WorkID, "source", p.SourceID, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 { // row already current (refresh no-op)
		*unchanged++
		return
	}
	*written++
	w.touched = append(w.touched, p.WorkID)
}

// popPlannedRow is one decided catalog_work_popularity write.
type popPlannedRow struct {
	WorkID   int64
	SourceID int16
	Metric   int16
	Value    int64
}

// writePopularity is the popularity twin of write: the change-detected upsert on
// the (work_id, source_id, metric) key.
func (w *writer) writePopularity(ctx context.Context, p popPlannedRow, apply bool) {
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
		slog.Warn("write popularity", "work", p.WorkID, "source", p.SourceID, "metric", p.Metric, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 { // row already current (refresh no-op)
		w.stats.PopUnchanged++
		return
	}
	w.stats.PopWritten++
	w.touched = append(w.touched, p.WorkID)
}
