package jobs

import (
	"context"
	"log/slog"
	"time"
)

// staleMaxAge: a `running` row older than this at startup is reaped to
// failed. Far above our longest job (full sync-vndb ~20min) so an
// in-flight run on another replica is never mis-reaped.
const staleMaxAge = 6 * time.Hour

// StartScheduler launches one goroutine per auto-scheduled job. Jobs
// without a schedule are registered but only run via admin trigger.
// Mirrors app.StartCleanup: call from cmd/oauth setupRoutes with the
// cleanup ctx; goroutines stop on ctx cancel.
//
// Single-flight across replicas is handled by the runner's advisory lock,
// so it is safe for every replica to run its own scheduler.
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

// runLoop sleeps until the job's next fire time, runs it, repeats. next is
// always recomputed from "now" after a run, so a long run never stacks the
// next tick (and the advisory lock would skip an overlap anyway).
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
