package jobs

import (
	"context"
	"time"

	jobmodel "api/internal/jobs/model"
	"api/pkg/config"
)

const (
	TriggerSchedule = jobmodel.TriggerSchedule
	TriggerAdmin    = jobmodel.TriggerAdmin
)

type Summary map[string]any

type RunFunc func(ctx context.Context, cfg *config.Config) (Summary, error)

type Job struct {
	Name     string
	Schedule Schedule
	Desc     string
	Run      RunFunc
}

type Schedule struct {
	DailyAt string
	Every   time.Duration
}

func (s Schedule) Zero() bool {
	return s.DailyAt == "" && s.Every <= 0
}

func (s Schedule) Next(now time.Time) time.Time {
	if s.DailyAt != "" {
		t, err := time.Parse("15:04", s.DailyAt)
		if err != nil {
			return time.Time{}
		}
		cand := time.Date(now.Year(), now.Month(), now.Day(),
			t.Hour(), t.Minute(), 0, 0, now.Location())
		if !cand.After(now) {
			cand = cand.Add(24 * time.Hour)
		}
		return cand
	}
	if s.Every > 0 {
		return now.Add(s.Every)
	}
	return time.Time{}
}
