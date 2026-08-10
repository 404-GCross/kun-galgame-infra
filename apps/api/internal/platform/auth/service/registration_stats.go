package service

import (
	"context"
	"math"
	"time"

	"api/internal/platform/auth/dto"
)

const statsTimezone = "Asia/Shanghai"

func statsLocation() *time.Location { return time.FixedZone(statsTimezone, 8*3600) }

func round1(x float64) float64 { return math.Round(x*10) / 10 }

func (s *AdminService) RegistrationStats(ctx context.Context, days int) (*dto.RegistrationStatsResponse, error) {
	if days < 1 {
		days = 30
	}
	if days > 90 {
		days = 90
	}

	loc := statsLocation()
	nowSh := time.Now().In(loc)
	today := time.Date(nowSh.Year(), nowSh.Month(), nowSh.Day(), 0, 0, 0, 0, loc)
	startCurrent := today.AddDate(0, 0, -(days - 1))
	startPrev := today.AddDate(0, 0, -(2*days - 1))

	counts, err := s.userRepo.RegistrationCountsByDay(ctx, startPrev, statsTimezone)
	if err != nil {
		return nil, err
	}
	totalAllTime, _ := s.userRepo.CountAll(ctx)

	todayKey := today.Format("2006-01-02")
	series := make([]dto.DailyRegistration, 0, days)
	rangeTotal, peakCount, todayCount := 0, 0, 0
	peakDate := ""
	for i := 0; i < days; i++ {
		key := startCurrent.AddDate(0, 0, i).Format("2006-01-02")
		c := counts[key]
		series = append(series, dto.DailyRegistration{Date: key, Count: c})
		rangeTotal += c
		if c > peakCount {
			peakCount, peakDate = c, key
		}
		if key == todayKey {
			todayCount = c
		}
	}

	prevTotal := 0
	for i := 0; i < days; i++ {
		prevTotal += counts[startPrev.AddDate(0, 0, i).Format("2006-01-02")]
	}
	growth := 0.0
	if prevTotal > 0 {
		growth = float64(rangeTotal-prevTotal) / float64(prevTotal) * 100
	}

	return &dto.RegistrationStatsResponse{
		RangeDays: days,
		Timezone:  statsTimezone,
		Series:    series,
		Summary: dto.RegistrationSummary{
			Total:           rangeTotal,
			DailyAverage:    round1(float64(rangeTotal) / float64(days)),
			PeakDate:        peakDate,
			PeakCount:       peakCount,
			Today:           todayCount,
			TotalAllTime:    totalAllTime,
			PrevPeriodTotal: prevTotal,
			GrowthPercent:   round1(growth),
		},
	}, nil
}

func (s *AdminService) HourlyRegistrations(ctx context.Context, date string) (*dto.HourlyStatsResponse, error) {
	loc := statsLocation()
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, err
	}
	dayEnd := day.AddDate(0, 0, 1)

	counts, err := s.userRepo.RegistrationCountsByHour(ctx, day, dayEnd, statsTimezone)
	if err != nil {
		return nil, err
	}

	series := make([]dto.HourlyRegistration, 0, 24)
	total := 0
	for h := 0; h < 24; h++ {
		c := counts[h]
		series = append(series, dto.HourlyRegistration{Hour: h, Count: c})
		total += c
	}
	return &dto.HourlyStatsResponse{
		Date:     date,
		Timezone: statsTimezone,
		Series:   series,
		Total:    total,
	}, nil
}
