// Package jobs is the in-process job registry + scheduler for
// kun-oauth-admin (design: docs/jobs/01-implementation-plan.md, option B,
// pure docker-compose). It only handles genuinely periodic / on-demand
// tasks (sync-vndb, image-gc, galgame-image-refping). One-shot
// migrations, continuous workers and HTTP services do NOT belong here.
package jobs

import (
	"context"
	"time"

	jobmodel "api/internal/jobs/model"
	"api/pkg/config"
)

// Trigger values, re-exported so callers (scheduler, admin handler) need
// not import the model subpackage just for these.
const (
	TriggerSchedule = jobmodel.TriggerSchedule
	TriggerAdmin    = jobmodel.TriggerAdmin
)

// Summary is a job's structured result, persisted to job_run.summary
// (jsonb). Free-form per job, not schema-validated.
type Summary map[string]any

// RunFunc is the actual work. It receives cfg and builds its own DB /
// clients on demand (jobs talk to different DBs — Galgame / Images — so
// the registry can't hold one shared handle). Must honor ctx cancel.
type RunFunc func(ctx context.Context, cfg *config.Config) (Summary, error)

// Job is one registered task.
type Job struct {
	// Name is the unique key, equal to the cmd name (e.g. "sync-vndb").
	Name string
	// Schedule zero value => not auto-scheduled (manual-trigger only).
	Schedule Schedule
	// Desc is a one-line human description for the admin UI.
	Desc string
	Run  RunFunc
}

// Schedule is deliberately minimal — daily-at or fixed-interval only.
// No cron five-field expression: pure single-process docker-compose with
// 3 daily jobs doesn't justify a cron-parser dependency. Revisit (e.g.
// robfig/cron/v3) only if a real cron expression is ever needed.
type Schedule struct {
	// DailyAt is "HH:MM" in the process local timezone. Takes precedence.
	DailyAt string
	// Every is a fixed interval; used only when DailyAt is empty.
	Every time.Duration
}

// Zero reports whether the schedule is unset (manual-trigger only).
func (s Schedule) Zero() bool {
	return s.DailyAt == "" && s.Every <= 0
}

// Next returns the next fire time strictly after now, or zero time if the
// schedule is unset or DailyAt is malformed (caller treats zero as "never").
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
