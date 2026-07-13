// fill-portrait-covers backfills a single PORTRAIT cover for wiki galgames that
// have none, so the portrait-first UI has a vertical image to show. Primary
// source is VNDB (vn.image, portrait decided from Kana `dims` before download);
// a tiny Bangumi fallback covers the few games VNDB cannot. New covers are
// inserted as NON-pinned candidates (sort_order = max+1) -- pinning is step 41.
//
// Logic lives in internal/jobs/portraitfill. This is a one-shot backfill CLI
// (not a scheduled job).
//
//	go run ./cmd/fill-portrait-covers                          # dry: full VNDB portrait-availability forecast
//	go run ./cmd/fill-portrait-covers --limit 1000 --apply \   # local slice: write wiki + LOCAL image service
//	    --image-base-url http://127.0.0.1:15006
//
// IMPORTANT: point --image-base-url at the LOCAL compose image service
// (MinIO-backed), NOT the native dev service whose S3 may target production.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/jobs/portraitfill"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run -- resolve candidates + VNDB metadata forecast only)")
	limit := flag.Int("limit", 0, "max candidates to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidates (for chunking)")
	apiGap := flag.Duration("api-gap", time.Second, "min delay between VNDB Kana API calls")
	downloadGap := flag.Duration("download-gap", 350*time.Millisecond, "min delay between image downloads (~3 req/s)")
	bgmGap := flag.Duration("bgm-gap", 700*time.Millisecond, "min delay between Bangumi API calls")
	bgmFallback := flag.Bool("bgm-fallback", true, "try Bangumi for candidates VNDB cannot cover")
	imageBaseURL := flag.String("image-base-url", "", "image_service base override (point at the LOCAL compose service, e.g. http://127.0.0.1:15006)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	sum, err := portraitfill.Run(context.Background(), cfg, portraitfill.Opts{
		Apply:        *apply,
		Limit:        *limit,
		Offset:       *offset,
		APIGap:       *apiGap,
		DownloadGap:  *downloadGap,
		BGMGap:       *bgmGap,
		BGMFallback:  *bgmFallback,
		ImageBaseURL: *imageBaseURL,
	})
	if sum != nil {
		slog.Info("fill-portrait-covers summary", "summary", sum)
	}
	if err != nil {
		slog.Error("fill-portrait-covers", "error", err)
		os.Exit(1)
	}
}
