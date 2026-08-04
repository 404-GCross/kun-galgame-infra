// backfill-vndb-covers fills catalog-native cover rows for galgame works that
// carry an EXACT VNDB anchor and show NO cover at all. For each such work it
// asks the public VNDB API (https://api.vndb.org/kana/vn) what cover the
// anchored vn has, downloads those bytes from t.vndb.org, uploads them to the
// catalog image scope and writes ONE catalog_work_cover row —
// portrait_pinned from the cover's own shape, sexual/violence from VNDB's own
// per-image votes.
//
// Logic lives in internal/jobs/vndbcovers, reusing the step-55
// (internal/jobs/dlsitemedia) upload/retry/refping machinery. Idempotent: a
// work with any cover is not a candidate, so a re-run writes nothing.
//
// IMPORTANT: --dsn is REQUIRED and must point at the rehearsal copy locally
// (kun_catalog_rehearsal) — NEVER the live kun_catalog. The acceptance-tester
// points --dsn at the production catalog for the real run.
//
//	# dry: per-work forecast (image found/missing, shape, ratings) + totals
//	go run ./cmd/backfill-vndb-covers \
//	    --dsn "host=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# dry, restricted to an explicit work list
//	go run ./cmd/backfill-vndb-covers --dsn "..." --ids 1,2,3
//
//	# apply against the LOCAL image service + catalog client creds
//	KUN_CATALOG_IMAGE_CLIENT_ID=... KUN_CATALOG_IMAGE_CLIENT_SECRET=... \
//	go run ./cmd/backfill-vndb-covers --apply --limit 20 \
//	    --dsn "..." --ids 1,2,3 --image-base-url http://127.0.0.1:9278
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/vndbcovers"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry-run forecast only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the production run")
	ids := flag.String("ids", "", "restrict to these catalog_work ids (comma separated); the anchor / no-cover predicates still apply")
	limit := flag.Int("limit", 0, "max covers to upload in --apply (0 = all); the dry-run forecast always covers the whole population")
	offset := flag.Int("offset", 0, "skip this many candidate works (for chunking)")
	imageBaseURL := flag.String("image-base-url", "", "image_service base override (point at the LOCAL dev service, e.g. http://127.0.0.1:9278)")
	uploadGap := flag.Duration("upload-gap", 0, "min delay between uploads (0 = none; raise for a gentle production sweep)")
	apiBase := flag.String("vndb-api-base", "", "VNDB API base override (default https://api.vndb.org/kana)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	workIDs, err := vndbcovers.ParseIDs(*ids)
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

	stats, err := vndbcovers.Run(context.Background(), cfg, vndbcovers.Opts{
		DSN:          *dsn,
		Apply:        *apply,
		Limit:        *limit,
		Offset:       *offset,
		IDs:          workIDs,
		ImageBaseURL: *imageBaseURL,
		UploadGap:    *uploadGap,
		APIBase:      *apiBase,
	})
	if stats != nil {
		slog.Info("backfill-vndb-covers summary", "result", stats.String())
	}
	if err != nil {
		slog.Error("backfill-vndb-covers", "error", err)
		os.Exit(1)
	}
}
