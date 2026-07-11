// build-galgame-stats recomputes the cross-source galgame statistics snapshots
// (galgame_stats) wholesale + idempotently from the wiki DB narrow tables:
// release-year distribution, per-source score histograms, per-year averages,
// and coverage. Applies by default (a safe snapshot upsert); --dry-run computes
// + logs without writing. No public surface is built here — that is step 34.
//
// Thin shell: logic lives in internal/jobs/galgamestats (single source of
// truth) so this CLI and the scheduled "build-galgame-stats" job (05:45) run
// identical code.
//
//	go run ./cmd/build-galgame-stats             # recompute + write
//	go run ./cmd/build-galgame-stats --dry-run   # compute + log, no writes
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "compute + log only, no writes (default: apply)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	if _, err := jobs.RunBuildGalgameStats(context.Background(), cfg, jobs.BuildGalgameStatsOpts{
		Apply: !*dryRun,
	}); err != nil {
		slog.Error("build-galgame-stats", "error", err)
		os.Exit(1)
	}
}
