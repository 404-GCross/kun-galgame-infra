// image-gc is the TTL lifecycle worker (cold-candidate report →
// soft-delete >365d unreferenced → hard-delete soft-deleted >30d).
//
// Thin shell: logic lives in internal/jobs (single source of truth) so
// the in-process scheduler and this CLI run identical code. Run daily,
// e.g.:
//
//	30 3 * * *  /usr/local/bin/kun-image-gc      # if scheduling externally
//
// Flags:
//
//	--cold-days=60     soft threshold to report as cold-storage candidates
//	--soft-days=365    age (since last_referenced_at) to soft-delete at
//	--hard-days=30     age (since deleted_at) to hard-delete at
//	--dry-run          log only, do not mutate DB or S3
//	--max=10000        max rows per phase per run
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
	coldDays := flag.Int("cold-days", 60, "days since last_referenced_at to report as cold-storage candidates")
	softDays := flag.Int("soft-days", 365, "days since last_referenced_at to soft-delete at")
	hardDays := flag.Int("hard-days", 30, "days since deleted_at to hard-delete at")
	dryRun := flag.Bool("dry-run", false, "log only, do not mutate")
	maxPerRun := flag.Int("max", 10000, "max rows per phase per run")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	summary, err := jobs.RunImageGC(context.Background(), cfg, jobs.ImageGCOpts{
		ColdDays:  *coldDays,
		SoftDays:  *softDays,
		HardDays:  *hardDays,
		DryRun:    *dryRun,
		MaxPerRun: *maxPerRun,
	})
	if err != nil {
		slog.Error("image-gc failed", "err", err)
		os.Exit(1)
	}
	slog.Info("image-gc run complete", "summary", summary)
}
