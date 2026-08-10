package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/labellogos"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	sourceName := flag.String("source", "", "which upstream supplies the bytes: bangumi (brand logos) or cien (creator avatars) — REQUIRED")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	mirrorDir := flag.String("mirror-dir", "", "local mirror root (<dir>/<external_id>/<logo|avatar>.<ext> + <dir>/dims.jsonl) — REQUIRED to apply")
	apply := flag.Bool("apply", false, "upload and write (default: dry-run forecast only)")
	limit := flag.Int("limit", 0, "max labels to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many labels first")
	idsOut := flag.String("ids-out", "", "write the distinct external ids still needing bytes to this file (the crawler's --ids-file)")
	auditOut := flag.String("audit-out", "", "write the falsification set (labels anchored to BOTH bangumi and cien) as CSV")
	imageBase := flag.String("image-base", "", "image_service base URL override (local dev)")
	gap := flag.Duration("upload-gap", 0, "min delay between uploads (raise for a long production sweep)")
	workers := flag.Int("workers", 1, "how many labels upload concurrently (1 = serial)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	source, err := labellogos.ParseSource(*sourceName)
	if err != nil {
		slog.Error("backfill-label-logos", "error", err)
		os.Exit(1)
	}

	st, err := labellogos.Run(context.Background(), cfg, labellogos.Opts{
		Source: source, DSN: *dsn, MirrorDir: *mirrorDir, Apply: *apply,
		Limit: *limit, Offset: *offset,
		UploadGap: *gap, ImageBase: *imageBase, Workers: *workers,
		IDsOut: *idsOut, AuditOut: *auditOut,
	})
	if st != nil {
		slog.Info("backfill-label-logos done", "apply", *apply, "result", st.String())
	}
	if err != nil {
		slog.Error("backfill-label-logos", "error", err)
		os.Exit(1)
	}
}
