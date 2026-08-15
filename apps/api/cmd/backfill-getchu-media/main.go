package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/jobs/getchumedia"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "upload and write (default: dry-run forecast only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	getchuDSN := flag.String("getchu-dsn", "", "getchu staging DSN — REQUIRED")
	mirrorDir := flag.String("mirror-dir", "", "local mirror root — REQUIRED to apply")
	limit := flag.Int("limit", 0, "max works to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many works first")
	maxPerWork := flag.Int("max-per-work", 0, "cap the gallery size per work (0 = no cap)")
	gap := flag.Duration("upload-gap", 0, "min delay between uploads (raise for a long production sweep)")
	workers := flag.Int("workers", 1, "how many WORKS upload concurrently (1 = serial)")
	imageBase := flag.String("image-base", "", "image_service base URL override (local dev)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	st, err := getchumedia.Run(context.Background(), cfg, getchumedia.Opts{
		DSN: *dsn, GetchuDSN: *getchuDSN, MirrorDir: *mirrorDir, Apply: *apply,
		Limit: *limit, Offset: *offset, MaxPerWork: *maxPerWork,
		UploadGap: *gap, ImageBase: *imageBase, Workers: *workers,
	})
	if st != nil {
		slog.Info("backfill-getchu-media done", "apply", *apply, "result", st.String())
	}
	if err != nil {
		slog.Error("backfill-getchu-media", "error", err)
		os.Exit(1)
	}
	_ = time.Now
	if st != nil && st.Errors > 0 {
		os.Exit(1)
	}
}
