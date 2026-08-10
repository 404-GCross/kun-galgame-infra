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
	batch := flag.Int("batch", 1000, "hashes per meta-batch probe (max 1000)")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall run timeout")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	summary, err := jobs.RunImageRefAudit(context.Background(), cfg, jobs.ImageRefAuditOpts{
		Batch:   *batch,
		Timeout: *timeout,
	})
	if err != nil {
		slog.Error("image-ref-audit failed", "summary", summary, "err", err)
		os.Exit(1)
	}
	slog.Info("image-ref-audit complete", "summary", summary)
}
