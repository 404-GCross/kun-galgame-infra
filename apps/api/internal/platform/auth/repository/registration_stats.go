package repository

import (
	"context"
	"time"

	"api/internal/platform/auth/model"
)

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

func (r *UserRepository) CountAll(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Count(&n).Error
	return n, err
}
