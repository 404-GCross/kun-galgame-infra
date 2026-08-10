package jobs

import (
	"context"
	"log/slog"
	"time"
)

const staleMaxAge = 6 * time.Hour

func StartScheduler(ctx context.Context, reg *Registry, runner *Runner) {
	runner.ReapStale(ctx, staleMaxAge)

	scheduled := 0
	for _, job := range reg.List() {
		if job.Schedule.Zero() {
			slog.Info("jobs: registered (manual-only)", "job", job.Name)
			continue
		}
		next := job.Schedule.Next(time.Now())
		slog.Info("jobs: scheduled", "job", job.Name, "next", next.Format(time.RFC3339))
		scheduled++
		go runLoop(ctx, job, runner)
	}
	slog.Info("jobs: scheduler started", "total", len(reg.List()), "auto", scheduled)
}

func runLoop(ctx context.Context, job Job, runner *Runner) {
	for {
		next := job.Schedule.Next(time.Now())
		if next.IsZero() {
			slog.Error("jobs: bad schedule, loop exiting", "job", job.Name)
			return
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			slog.Info("jobs: scheduler loop stopped", "job", job.Name)
			return
		case <-timer.C:
			runner.Run(ctx, job, TriggerSchedule)
		}
	}
}
