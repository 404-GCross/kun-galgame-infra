package releasemeta

import (
	"context"
	"log/slog"

	"gorm.io/gorm"
)

// writer applies planned fills with the FILL-EMPTY-ONLY guard re-asserted
// inside the UPDATE itself: the WHERE clause requires the target scalar to
// still be empty at write time, so a non-zero value is never overwritten no
// matter what the candidate query saw (step-10 precedent). RowsAffected
// separates real fills from last-moment skips. NO upsert — dates and ratings
// are non-volatile, in deliberate contrast to step-62's refresh loop.
type writer struct {
	db    *gorm.DB
	stats *Stats
}

// fillDate fills one release's released_y/m/d trio (m/d nullable — partial
// dates are legal). filled/skipped point at the owning lane's counters.
func (w *writer) fillDate(ctx context.Context, releaseID int64, y int16, m, d *int16, apply bool, filled, skipped *int) {
	if !apply {
		return
	}
	res := w.db.WithContext(ctx).Exec(
		`UPDATE catalog_release SET released_y = ?, released_m = ?, released_d = ?
		 WHERE id = ? AND deleted_at IS NULL AND (released_y IS NULL OR released_y = 0)`,
		y, m, d, releaseID)
	if res.Error != nil {
		w.stats.Errors++
		slog.Warn("fill release date", "release", releaseID, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 { // row gained a date since candidate load — never overwrite
		*skipped++
		return
	}
	*filled++
}

// fillRating fills one work's content_rating (only ever called with a
// verdict > 0 — an explicit all-ages verdict keeps the row 0 and is counted,
// not written).
func (w *writer) fillRating(ctx context.Context, workID int64, rating int16, apply bool) {
	if !apply {
		return
	}
	res := w.db.WithContext(ctx).Exec(
		`UPDATE catalog_work SET content_rating = ?, updated_at = now()
		 WHERE id = ? AND deleted_at IS NULL AND content_rating = 0`,
		rating, workID)
	if res.Error != nil {
		w.stats.Errors++
		slog.Warn("fill content_rating", "work", workID, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 { // rated since candidate load — never overwrite
		w.stats.RatingSkippedNonEmpty++
		return
	}
	w.stats.RatingFilled++
}
