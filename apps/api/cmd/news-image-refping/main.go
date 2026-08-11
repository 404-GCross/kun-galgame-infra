package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/jobs"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	batch := flag.Int("batch", 1000, "hashes per reference-ping request (max 1000)")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall run timeout")
	dryRun := flag.Bool("dry-run", false, "log only, do not call image_service")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	summary, err := jobs.RunNewsImageRefping(context.Background(), cfg, jobs.NewsImageRefpingOpts{
		Batch:   *batch,
		Timeout: *timeout,
		DryRun:  *dryRun,
	})
	if err != nil {
		slog.Error("news-image-refping failed", "summary", summary, "err", err)
		os.Exit(1)
	}
	slog.Info("news-image-refping complete", "summary", summary)
}
