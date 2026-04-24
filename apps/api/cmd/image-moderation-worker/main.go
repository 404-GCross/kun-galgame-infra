// image-moderation-worker is the async moderation worker. Long-running
// process that polls the image_moderation_queue table and runs the
// configured moderation provider on each entry.
//
// Flags:
//   --provider=noop|aliyun|tencent   which provider to use (default noop)
//   --batch=20                       rows claimed per poll
//   --poll=15s                       interval between polls
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/image/moderation"
	"api/internal/platform/image/service"
	"api/internal/platform/image/storage"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	providerName := flag.String("provider", "noop", "moderation provider (noop only in V3.0)")
	batch := flag.Int("batch", 20, "rows per poll")
	pollInterval := flag.Duration("poll", 15*time.Second, "poll interval")
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

	var provider moderation.Provider
	switch *providerName {
	case "noop", "":
		provider = moderation.NewNoop()
	default:
		slog.Error("unknown provider", "name", *providerName, "hint", "aliyun/tencent providers deferred to post-V3.0")
		os.Exit(1)
	}

	worker := service.NewModerationWorker(db.DB(), s3, provider)
	worker.BatchSize = *batch
	worker.PollEvery = *pollInterval

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("moderation worker launched", "provider", provider.Name())
	if err := worker.Run(ctx); err != nil && err != context.Canceled {
		slog.Error("worker exit", "err", err)
		os.Exit(1)
	}
	slog.Info("moderation worker stopped cleanly")
}
