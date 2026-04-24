// image-gc is the TTL lifecycle worker. Run it as a cron (e.g. daily):
//
//   # Daily at 03:30
//   30 3 * * *  /usr/local/bin/kun-image-gc
//
// Flags:
//   --cold-days=60     soft threshold to report as cold-storage candidates
//   --soft-days=365    age (since last_referenced_at) to soft-delete at
//   --hard-days=30     age (since deleted_at) to hard-delete at
//   --dry-run          log only, do not mutate DB or S3
//   --max=10000        max rows per phase per run
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/image/repository"
	"api/internal/platform/image/service"
	"api/internal/platform/image/storage"
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

	db, err := database.NewPostgresDB(cfg.ImagesDatabase)
	if err != nil {
		slog.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	s3, err := storage.NewClient(cfg.ImageS3)
	if err != nil {
		slog.Error("s3 init", "err", err)
		os.Exit(1)
	}

	imgRepo := repository.NewImageRepository(db.DB())
	gc := service.NewGCService(db.DB(), s3, imgRepo)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	summary, err := gc.Run(ctx, service.GCConfig{
		ColdAfter:    time.Duration(*coldDays) * 24 * time.Hour,
		SoftDelAfter: time.Duration(*softDays) * 24 * time.Hour,
		HardDelAfter: time.Duration(*hardDays) * 24 * time.Hour,
		DryRun:       *dryRun,
		MaxPerRun:    *maxPerRun,
	})
	if err != nil {
		slog.Error("gc run", "err", err)
		os.Exit(1)
	}

	b, _ := json.MarshalIndent(summary, "", "  ")
	slog.Info("gc run complete", "summary", string(b))
}
