package releasemeta

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/repository"
	"api/internal/platform/provenance"

	"gorm.io/gorm"
)

type writer struct {
	db      *gorm.DB
	stats   *Stats
	touched []int64
}

func (w *writer) touch(ctx context.Context) error {
	return repository.TouchWorks(ctx, w.db, w.touched)
}

func (w *writer) fillDate(ctx context.Context, releaseID int64, y int16, m, d *int16, apply bool, filled, skipped *int) {
	if !apply {
		return
	}
	var hosts []int64
	res := w.db.WithContext(ctx).Raw(
		`UPDATE catalog_release SET released_y = ?, released_m = ?, released_d = ?
		 WHERE id = ? AND deleted_at IS NULL AND (released_y IS NULL OR released_y = 0)
		   AND COALESCE(field_provenance -> 'released_y' -> 0 ->> 'source', '') NOT IN ?
		 RETURNING work_id`,
		y, m, d, releaseID, provenance.HumanSources()).Scan(&hosts)
	if res.Error != nil {
		w.stats.Errors++
		slog.Warn("fill release date", "release", releaseID, "err", res.Error)
		return
	}
	if len(hosts) == 0 {
		*skipped++
		return
	}
	w.touched = append(w.touched, hosts...)
	*filled++
}

func (w *writer) fillRating(ctx context.Context, workID int64, rating int16, apply bool) {
	if !apply {
		return
	}
	// content_rating = 0 cannot tell "nobody set it" from "a human ruled
	// all_ages", so the zero test on its own let an upstream r18 verdict
	// overwrite the human decision.
	res := w.db.WithContext(ctx).Exec(
		`UPDATE catalog_work SET content_rating = ?, updated_at = now()
		 WHERE id = ? AND deleted_at IS NULL AND content_rating = 0
		   AND COALESCE(field_provenance -> 'content_rating' -> 0 ->> 'source', '') NOT IN ?`,
		rating, workID, provenance.HumanSources())
	if res.Error != nil {
		w.stats.Errors++
		slog.Warn("fill content_rating", "work", workID, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 {
		w.stats.RatingSkippedNonEmpty++
		return
	}
	w.stats.RatingFilled++
}
