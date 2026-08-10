package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"api/internal/jobs/repincovers"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: report only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	ids := flag.String("ids", "", "restrict to these catalog_work ids (comma separated)")
	limit := flag.Int("limit", 0, "max works to act on (0 = all); the report always covers everything")
	planOut := flag.String("plan-out", "", "write the full plan to this CSV path (old/new kind, source, size, URL)")
	exportDir := flag.String("export-dir", "", "download the under-target winners here for upscale-bench")
	reinjectDir := flag.String("reinject-dir", "", "upload the upscaled products found in this directory")
	purge := flag.Bool("purge-bad-upscales", false, "delete super-resolution rows whose kind says they enlarge a box back / disc / booklet / spine")
	imageBaseURL := flag.String("image-base-url", "", "image_service base override (e.g. http://127.0.0.1:9278)")
	uploadGap := flag.Duration("upload-gap", 0, "min delay between uploads (0 = none)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	workIDs, err := parseIDs(*ids)
	if err != nil {
		slog.Error("parse --ids", "error", err)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	stats, err := repincovers.Run(context.Background(), cfg, repincovers.Opts{
		DSN:              *dsn,
		Apply:            *apply,
		IDs:              workIDs,
		Limit:            *limit,
		PlanOut:          *planOut,
		ExportDir:        *exportDir,
		ReinjectDir:      *reinjectDir,
		PurgeBadUpscales: *purge,
		ImageBaseURL:     *imageBaseURL,
		UploadGap:        *uploadGap,
	})
	if stats != nil {
		slog.Info("repin-portrait-covers summary", "result", stats.String())
	}
	if err != nil {
		slog.Error("repin-portrait-covers", "error", err)
		os.Exit(1)
	}
}

func parseIDs(s string) ([]int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}
