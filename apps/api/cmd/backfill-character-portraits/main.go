// backfill-character-portraits uploads catalog CHARACTER portraits into the
// image service (site "catalog", preset "character") and writes the returned
// content hash to catalog_character.image_hash. It consumes the step-47 output
// src_vndb.portrait_backfill (one in-gate VNDB portrait per catalog character,
// already filtered to sexual/violence ≤ 100) and reads the portrait bytes from a
// LOCAL VNDB image mirror (rsync ch/ — this job never dials t.vndb.org).
//
// Logic lives in internal/jobs/charportraits. Idempotent: a character whose
// image_hash is already set is skipped before any byte read, so a re-run writes
// nothing (skipped_has_hash == whole window).
//
// IMPORTANT: --dsn is REQUIRED and must point at the rehearsal copy locally
// (kun_catalog_rehearsal) — NEVER the live kun_catalog. The acceptance-tester
// points it at the production catalog for the real run.
//
//	# dry: forecast candidates + local-file availability
//	go run ./cmd/backfill-character-portraits \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable" \
//	    --vndb-image-dir ./vndb-img
//
//	# apply a local slice against the LOCAL compose/dev image service
//	KUN_CATALOG_IMAGE_CLIENT_ID=... KUN_CATALOG_IMAGE_CLIENT_SECRET=... \
//	go run ./cmd/backfill-character-portraits --apply --limit 300 \
//	    --dsn "...dbname=kun_catalog_rehearsal..." \
//	    --vndb-image-dir ./vndb-img --image-base-url http://127.0.0.1:9278
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/charportraits"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run — resolve candidates + local-file forecast only)")
	limit := flag.Int("limit", 0, "max portrait_backfill rows to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many rows (for chunking)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the production run")
	vndbImageDir := flag.String("vndb-image-dir", "", "local rsync mirror root containing ch/ (bytes are read from here) [required]")
	imageBaseURL := flag.String("image-base-url", "", "image_service base override (point at the LOCAL compose/dev service, e.g. http://127.0.0.1:9278)")
	uploadGap := flag.Duration("upload-gap", 0, "min delay between uploads (0 = none; raise for a gentle production sweep)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	sum, err := charportraits.Run(context.Background(), cfg, charportraits.Opts{
		Apply:        *apply,
		Limit:        *limit,
		Offset:       *offset,
		DSN:          *dsn,
		VNDBImageDir: *vndbImageDir,
		ImageBaseURL: *imageBaseURL,
		UploadGap:    *uploadGap,
	})
	if sum != nil {
		slog.Info("backfill-character-portraits summary", "summary", sum)
	}
	if err != nil {
		slog.Error("backfill-character-portraits", "error", err)
		os.Exit(1)
	}
}
