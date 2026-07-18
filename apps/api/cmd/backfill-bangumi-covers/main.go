// backfill-bangumi-covers fills catalog-native PORTRAIT cover rows for BODYLESS
// galgame works from Bangumi (BGM 竖封轨 Phase 3, refs/proj/56c). For each
// bodyless galgame work carrying an EXACT Bangumi anchor
// (catalog_external_ref matched_by='rule:bgm-title-year') whose fetched cover is
// VERTICAL (dims h > w) it uploads that cover to the catalog image scope and
// writes one catalog_work_cover row with portrait_pinned=true, source=bangumi —
// the vertical cover kungal/moyu read. Image BYTES are read from the LOCAL
// mirror produced by kun-bangumi-api (Phase 2); this job NEVER dials Bangumi.
//
// Logic lives in internal/jobs/bangumicovers, reusing the step-55
// (internal/jobs/dlsitemedia) upload/retry/idempotency/refping machinery.
// Idempotent: a work that already has a Bangumi cover is skipped before any byte
// read, so a re-run writes nothing.
//
// IMPORTANT: --dsn is REQUIRED and must point at the rehearsal copy locally
// (kun_catalog_rehearsal) — NEVER the live kun_catalog. The acceptance-tester
// points --dsn at the production catalog for the real run.
//
//	# dry: forecast (exact anchors / portrait / landscape-skip / no-dims / missing)
//	go run ./cmd/backfill-bangumi-covers \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable" \
//	    --bangumi-mirror ./bangumi-mirror
//
//	# apply against the LOCAL image service + catalog client creds
//	KUN_CATALOG_IMAGE_CLIENT_ID=... KUN_CATALOG_IMAGE_CLIENT_SECRET=... \
//	go run ./cmd/backfill-bangumi-covers --apply --limit 20 \
//	    --dsn "...dbname=kun_catalog_rehearsal..." \
//	    --bangumi-mirror ./bangumi-mirror --image-base-url http://127.0.0.1:9278
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/bangumicovers"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry-run forecast only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the production run")
	mirror := flag.String("bangumi-mirror", "", "local mirror root — REQUIRED (<dir>/<subject_id>/cover.jpg + <dir>/dims.jsonl)")
	limit := flag.Int("limit", 0, "max candidate works to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate works (for chunking)")
	imageBaseURL := flag.String("image-base-url", "", "image_service base override (point at the LOCAL dev service, e.g. http://127.0.0.1:9278)")
	uploadGap := flag.Duration("upload-gap", 0, "min delay between uploads (0 = none; raise for a gentle production sweep)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	sum, err := bangumicovers.Run(context.Background(), cfg, bangumicovers.Opts{
		Apply:         *apply,
		Limit:         *limit,
		Offset:        *offset,
		DSN:           *dsn,
		BangumiMirror: *mirror,
		ImageBaseURL:  *imageBaseURL,
		UploadGap:     *uploadGap,
	})
	if sum != nil {
		slog.Info("backfill-bangumi-covers summary", "summary", sum)
	}
	if err != nil {
		slog.Error("backfill-bangumi-covers", "error", err)
		os.Exit(1)
	}
}
