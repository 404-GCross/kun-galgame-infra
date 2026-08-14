package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/figurecutout"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	slotName := flag.String("slot", "", "which column to swap: figure (figure_hash) or bust (image_hash) — REQUIRED")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	dir := flag.String("dir", "", "cutout output directory holding manifest.jsonl — REQUIRED")
	apply := flag.Bool("apply", false, "upload and swap (default: dry-run forecast only)")
	limit := flag.Int("limit", 0, "max cutouts to write (0 = all)")
	workers := flag.Int("workers", 4, "how many cutouts upload concurrently")
	gap := flag.Duration("upload-gap", 0, "min delay between uploads")
	imageBase := flag.String("image-base", "", "image_service base URL override (local dev)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	slot, err := figurecutout.ParseSlot(*slotName)
	if err != nil {
		slog.Error("backfill-figure-cutouts", "error", err)
		os.Exit(1)
	}

	st, err := figurecutout.Run(context.Background(), cfg, figurecutout.Opts{
		DSN: *dsn, Dir: *dir, Slot: slot, Apply: *apply, Limit: *limit,
		Workers: *workers, UploadGap: *gap, ImageBase: *imageBase,
	})
	if st != nil {
		slog.Info("backfill-figure-cutouts done", "apply", *apply, "result", st.String())
	}
	if err != nil {
		slog.Error("backfill-figure-cutouts", "error", err)
		os.Exit(1)
	}
	if st != nil && st.Errors > 0 {
		os.Exit(1)
	}
}
