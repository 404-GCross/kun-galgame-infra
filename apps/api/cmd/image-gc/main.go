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
