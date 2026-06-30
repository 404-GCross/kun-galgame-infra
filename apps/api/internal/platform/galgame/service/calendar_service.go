package service

import (
	"context"
	"time"

	"api/internal/platform/galgame/model"
)

// Release-calendar service methods. Thin pass-throughs to the repository — the
// HTTP-shaped concerns (month/year parsing, JST "now", ETag, cache headers)
// live in the handler. See docs/galgame_wiki/06-release-calendar-design.md.

func (s *GalgameService) CalendarMonthMeta(ctx context.Context, startDate, nextDate, contentLimit string, origLangs []string) (int64, time.Time, error) {
	return s.galgameRepo.CalendarMonthMeta(ctx, startDate, nextDate, contentLimit, origLangs)
}

func (s *GalgameService) CalendarMonth(ctx context.Context, startDate, nextDate, contentLimit string, origLangs []string) ([]model.Galgame, error) {
	return s.galgameRepo.CalendarMonth(ctx, startDate, nextDate, contentLimit, origLangs)
}

func (s *GalgameService) CalendarBounds(ctx context.Context, contentLimit string, origLangs []string) (minMonth, maxMonth string, err error) {
	return s.galgameRepo.CalendarBounds(ctx, contentLimit, origLangs)
}

func (s *GalgameService) CalendarYearPendingMeta(ctx context.Context, yearStart, yearNext, contentLimit string, origLangs []string) (int64, time.Time, error) {
	return s.galgameRepo.CalendarYearPendingMeta(ctx, yearStart, yearNext, contentLimit, origLangs)
}

func (s *GalgameService) CalendarYearPending(ctx context.Context, yearStart, yearNext, contentLimit string, origLangs []string) ([]model.Galgame, error) {
	return s.galgameRepo.CalendarYearPending(ctx, yearStart, yearNext, contentLimit, origLangs)
}

func (s *GalgameService) CalendarTBAMeta(ctx context.Context, contentLimit string, origLangs []string) (int64, time.Time, error) {
	return s.galgameRepo.CalendarTBAMeta(ctx, contentLimit, origLangs)
}

func (s *GalgameService) CalendarTBA(ctx context.Context, contentLimit string, origLangs []string) ([]model.Galgame, error) {
	return s.galgameRepo.CalendarTBA(ctx, contentLimit, origLangs)
}
