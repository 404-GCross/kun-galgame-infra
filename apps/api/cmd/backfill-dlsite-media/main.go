// backfill-dlsite-media fills the catalog-native media rows for galgame works
// from DLsite (media-aggregation wave, refs/proj/55 + refs/proj/125). For each
// galgame work reachable via a DLsite workno release anchor it writes an intro
// (from dlsite.works.page_json.parts), a cover (image_main), and screenshots
// (image_samples[]) — all under source_id=dlsite. Image BYTES are read from a
// LOCAL mirror produced by kun-dlsite-api's `mirror` command; this job NEVER
// dials DLsite.
//
// Two candidate lanes: BODYLESS works take all three kinds; CLAIMED works
// (site='galgame_wiki') take SCREENSHOTS ONLY, and only those that show no
// screenshot at all today (no wiki bridge row, no native row) — refs/proj/125.
// intro/cover on a claimed work stay refused (they are bridged at read time).
// The summary reports the lane split as candidates_bodyless / candidates_claimed.
//
// Logic lives in internal/jobs/dlsitemedia. Idempotent: a work whose media row
// already exists is skipped before any byte read, so a re-run writes nothing.
//
// IMPORTANT: --dsn (catalog) and --dlsite-dsn (staging) are REQUIRED and must
// point at the rehearsal copy locally (kun_catalog_rehearsal) — NEVER the live
// kun_catalog. The acceptance-tester points --dsn at the production catalog for
// the real run.
//
//	# dry: forecast all three media (candidates / mirror-hits / missing / etc.)
//	go run ./cmd/backfill-dlsite-media \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable" \
//	    --dlsite-dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=dlsite sslmode=disable" \
//	    --mirror-dir ./dlsite-mirror
//
//	# intro only (pure DB — no image service needed)
//	go run ./cmd/backfill-dlsite-media --apply --kind intro \
//	    --dsn "...dbname=kun_catalog_rehearsal..." --dlsite-dsn "...dbname=dlsite..."
//
//	# cover+screenshot against the LOCAL image service + catalog client creds
//	KUN_CATALOG_IMAGE_CLIENT_ID=... KUN_CATALOG_IMAGE_CLIENT_SECRET=... \
//	go run ./cmd/backfill-dlsite-media --apply --kind cover,screenshot --limit 20 \
//	    --dsn "...dbname=kun_catalog_rehearsal..." --dlsite-dsn "...dbname=dlsite..." \
//	    --mirror-dir ./dlsite-mirror --image-base-url http://127.0.0.1:9278
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/dlsitemedia"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry-run forecast only)")
	kind := flag.String("kind", "all", "which media to backfill: all | intro | cover | screenshot (comma-separated)")
	limit := flag.Int("limit", 0, "max candidate works to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many BODYLESS candidate works (for chunking); the claimed screenshot lane is always taken from its head — it self-resumes, so offsetting into it would skip works")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the production run")
	dlsiteDSN := flag.String("dlsite-dsn", "", "dlsite staging DSN — REQUIRED; reads product_json/page_json")
	mirrorDir := flag.String("mirror-dir", "", "local mirror root <root>/<workno>/<filename> (required for cover/screenshot)")
	imageBaseURL := flag.String("image-base-url", "", "image_service base override (point at the LOCAL dev service, e.g. http://127.0.0.1:9278)")
	uploadGap := flag.Duration("upload-gap", 0, "min delay between uploads (0 = none; raise for a gentle production sweep)")
	flag.Parse()

	kinds, err := dlsitemedia.ParseKinds(*kind)
	if err != nil {
		slog.Error("bad --kind", "error", err)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	sum, err := dlsitemedia.Run(context.Background(), cfg, dlsitemedia.Opts{
		Apply:        *apply,
		Kinds:        kinds,
		Limit:        *limit,
		Offset:       *offset,
		DSN:          *dsn,
		DlsiteDSN:    *dlsiteDSN,
		MirrorDir:    *mirrorDir,
		ImageBaseURL: *imageBaseURL,
		UploadGap:    *uploadGap,
	})
	if sum != nil {
		slog.Info("backfill-dlsite-media summary", "summary", sum)
	}
	if err != nil {
		slog.Error("backfill-dlsite-media", "error", err)
		os.Exit(1)
	}
}
