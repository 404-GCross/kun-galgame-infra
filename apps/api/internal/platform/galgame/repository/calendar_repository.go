package repository

import (
	"context"
	"time"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// Release-calendar queries. See docs/galgame_wiki/06-release-calendar-design.md.
//
// All of these serve the precision-aware month view: published (status=0) games
// of a given release_precision, optionally within a half-open date window. They
// ride idx_galgame_calendar (release_date, release_precision, id) WHERE status=0.

// calendarFilter is the internal predicate shared by the meta + list queries.
type calendarFilter struct {
	precisions   []string // release_precision IN (...)
	startDate    string   // "" = no lower bound; else "YYYY-MM-DD" (>=)
	nextDate     string   // "" = no upper bound; else "YYYY-MM-DD" (<, half-open)
	contentLimit string   // "" = any
}

func (r *GalgameRepository) calendarBase(ctx context.Context, f calendarFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("status = 0").
		Where("release_precision IN ?", f.precisions)
	// Dates pass as YYYY-MM-DD strings (NOT time.Time): a time.Time arg forces
	// an implicit timestamptz coercion that drops the btree index — same lesson
	// as List. A string literal compares directly against the `date` column.
	if f.startDate != "" {
		q = q.Where("release_date >= ?", f.startDate)
	}
	if f.nextDate != "" {
		q = q.Where("release_date < ?", f.nextDate)
	}
	if f.contentLimit != "" {
		q = q.Where("content_limit = ?", f.contentLimit)
	}
	return q
}

// calendarMeta returns (count, maxUpdated) for the filter — the cheap ETag basis
// so a matching If-None-Match can 304 without the heavy item+preload query.
func (r *GalgameRepository) calendarMeta(ctx context.Context, f calendarFilter) (int64, time.Time, error) {
	var row struct {
		Count      int64
		MaxUpdated time.Time
	}
	err := r.calendarBase(ctx, f).
		Select("COUNT(*) AS count, COALESCE(MAX(updated), to_timestamp(0)) AS max_updated").
		Scan(&row).Error
	return row.Count, row.MaxUpdated, err
}

// calendarList runs the item query (covers + officials preloaded, effective
// banner populated), ordered by `order`.
func (r *GalgameRepository) calendarList(ctx context.Context, f calendarFilter, order string) (items []model.Galgame, err error) {
	defer func() {
		for i := range items {
			model.PopulateEffectiveBanner(&items[i])
		}
	}()
	err = r.calendarBase(ctx, f).
		Order(order).
		Preload("Official.Official").
		Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Find(&items).Error
	return items, err
}

// monthOrder: ascending by date; within a day, exact-day before day-unknown; id
// breaks ties. Uses the bare release_precision column (not a boolean expression)
// so the order is fully covered by idx_galgame_calendar (release_date,
// release_precision, id) — no Sort node. Relies on monthPrecisions = {day,month}
// and 'day' < 'month' lexically, which yields the same order as `(=
// 'month') ASC` would. See design §5.
const monthOrder = "release_date ASC, release_precision ASC, id ASC"

var monthPrecisions = []string{string(model.PrecisionDay), string(model.PrecisionMonth)}

// CalendarMonthMeta / CalendarMonth: day+month-precision games in the half-open
// [startDate, nextDate) month window.
func (r *GalgameRepository) CalendarMonthMeta(ctx context.Context, startDate, nextDate, contentLimit string) (int64, time.Time, error) {
	return r.calendarMeta(ctx, calendarFilter{precisions: monthPrecisions, startDate: startDate, nextDate: nextDate, contentLimit: contentLimit})
}

func (r *GalgameRepository) CalendarMonth(ctx context.Context, startDate, nextDate, contentLimit string) ([]model.Galgame, error) {
	return r.calendarList(ctx, calendarFilter{precisions: monthPrecisions, startDate: startDate, nextDate: nextDate, contentLimit: contentLimit}, monthOrder)
}

// CalendarBounds returns the earliest and latest month ("YYYY-MM") that hold a
// published day/month-precision release for the content limit — the navigable
// range, so a client can disable prev/next at the edges. Empty strings when
// there are no dated releases. Rides idx_galgame_calendar (MIN/MAX on the
// leading release_date column).
func (r *GalgameRepository) CalendarBounds(ctx context.Context, contentLimit string) (minMonth, maxMonth string, err error) {
	var row struct {
		MinMonth *string
		MaxMonth *string
	}
	// No date window: calendarBase gives status=0 + day/month precision + content
	// limit; MIN/MAX walk idx_galgame_calendar from each end.
	if err = r.calendarBase(ctx, calendarFilter{precisions: monthPrecisions, contentLimit: contentLimit}).
		Select("to_char(MIN(release_date), 'YYYY-MM') AS min_month, to_char(MAX(release_date), 'YYYY-MM') AS max_month").
		Scan(&row).Error; err != nil {
		return "", "", err
	}
	if row.MinMonth != nil {
		minMonth = *row.MinMonth
	}
	if row.MaxMonth != nil {
		maxMonth = *row.MaxMonth
	}
	return minMonth, maxMonth, nil
}

// CalendarYearPendingMeta / CalendarYearPending: year-precision games (month
// unknown) within the given year — the "month TBD" bucket.
func (r *GalgameRepository) CalendarYearPendingMeta(ctx context.Context, yearStart, yearNext, contentLimit string) (int64, time.Time, error) {
	return r.calendarMeta(ctx, calendarFilter{precisions: []string{string(model.PrecisionYear)}, startDate: yearStart, nextDate: yearNext, contentLimit: contentLimit})
}

func (r *GalgameRepository) CalendarYearPending(ctx context.Context, yearStart, yearNext, contentLimit string) ([]model.Galgame, error) {
	return r.calendarList(ctx, calendarFilter{precisions: []string{string(model.PrecisionYear)}, startDate: yearStart, nextDate: yearNext, contentLimit: contentLimit}, "id ASC")
}

// CalendarTBAMeta / CalendarTBA: the global "release date pending" bucket
// (announced, no date). Newest-touched first.
func (r *GalgameRepository) CalendarTBAMeta(ctx context.Context, contentLimit string) (int64, time.Time, error) {
	return r.calendarMeta(ctx, calendarFilter{precisions: []string{string(model.PrecisionTBA)}, contentLimit: contentLimit})
}

func (r *GalgameRepository) CalendarTBA(ctx context.Context, contentLimit string) ([]model.Galgame, error) {
	return r.calendarList(ctx, calendarFilter{precisions: []string{string(model.PrecisionTBA)}, contentLimit: contentLimit}, "updated DESC, id DESC")
}
