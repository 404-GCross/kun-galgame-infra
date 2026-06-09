package repository

import (
	"context"
	"time"

	"api/internal/platform/auth/model"
)

// RegistrationCountsByDay returns the number of users created on each calendar
// day, keyed by "YYYY-MM-DD" in timezone `tz`, for users created at/after
// `since`. Days with zero registrations are absent from the map — the caller
// 0-fills the contiguous range. `tz` is bound as a query parameter, so it is
// injection-safe even though callers pass a constant.
func (r *UserRepository) RegistrationCountsByDay(ctx context.Context, since time.Time, tz string) (map[string]int, error) {
	type row struct {
		Day   string
		Count int
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&model.User{}).
		Select("to_char((created_at AT TIME ZONE ?), 'YYYY-MM-DD') AS day, count(*) AS count", tz).
		Where("created_at >= ?", since).
		Group("day").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, rw := range rows {
		out[rw.Day] = rw.Count
	}
	return out, nil
}

// RegistrationCountsByHour returns user counts bucketed by hour-of-day (0-23 in
// timezone `tz`), keyed by hour, for users created in [dayStart, dayEnd).
func (r *UserRepository) RegistrationCountsByHour(ctx context.Context, dayStart, dayEnd time.Time, tz string) (map[int]int, error) {
	type row struct {
		Hour  int
		Count int
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&model.User{}).
		Select("extract(hour from (created_at AT TIME ZONE ?))::int AS hour, count(*) AS count", tz).
		Where("created_at >= ? AND created_at < ?", dayStart, dayEnd).
		Group("hour").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int]int, len(rows))
	for _, rw := range rows {
		out[rw.Hour] = rw.Count
	}
	return out, nil
}

// CountAll returns the total number of user rows (all-time).
func (r *UserRepository) CountAll(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Count(&n).Error
	return n, err
}
