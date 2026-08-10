package workratings

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type writer struct {
	db      *gorm.DB
	stats   *Stats
	touched []int64
}

func (w *writer) touch(ctx context.Context) error {
	return repository.TouchWorks(ctx, w.db, w.touched)
}

type plannedRow struct {
	WorkID    int64
	SourceID  int16
	Score     float64
	VoteCount int
	Rank      *int
}

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
	if res.RowsAffected == 0 {
		*unchanged++
		return
	}
	*written++
	w.touched = append(w.touched, p.WorkID)
}

type popPlannedRow struct {
	WorkID   int64
	SourceID int16
	Metric   int16
	Value    int64
}

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
	if res.RowsAffected == 0 {
		w.stats.PopUnchanged++
		return
	}
	w.stats.PopWritten++
	w.touched = append(w.touched, p.WorkID)
}
