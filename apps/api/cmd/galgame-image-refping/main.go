// galgame-image-refping keeps galgame wiki banner images alive in
// image_service. image_service is TTL-driven: a hash unreferenced for
// >365d is soft-deleted, then physically removed 30d later (see
// internal/platform/image/service/gc.go). galgame banners are
// set-once/rarely-changed — without a periodic ping they only ever get
// the single TTL touch from their original upload and silently vanish
// ~13 months later.
//
// This is the per-caller daily ping mandated by
// docs/image_service/04-migration-plan.md (调用方 cron 清单) and
// 06-integration-guide.md §七/§九. Run it as a daily cron, e.g.:
//
//	# Daily at 04:00
//	0 4 * * *  /usr/local/bin/kun-galgame-image-refping
//
// Flags:
//
//	--batch=1000    hashes per reference-ping request (image_service caps at 1000)
//	--timeout=30m   overall run timeout
//	--dry-run       collect + log the hash count, do not call image_service
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/imageclient"
	"api/pkg/logger"
)

func main() {
	batch := flag.Int("batch", 1000, "hashes per reference-ping request (max 1000)")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall run timeout")
	dryRun := flag.Bool("dry-run", false, "log only, do not call image_service")
	flag.Parse()

	if *batch < 1 || *batch > 1000 {
		*batch = 1000 // image_service / SDK reject batches > 1000
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// galgame.banner_image_hash is the authoritative reference. Banner is
	// the only image_service-backed field on the wiki side; avatars etc.
	// live on the OAuth service and are pinged by that caller separately.
	var hashes []string
	if err := db.DB().WithContext(ctx).
		Raw(`SELECT DISTINCT banner_image_hash
		     FROM galgame
		     WHERE banner_image_hash IS NOT NULL AND banner_image_hash <> ''`).
		Scan(&hashes).Error; err != nil {
		slog.Error("collect banner hashes", "err", err)
		os.Exit(1)
	}

	slog.Info("collected referenced banner hashes", "count", len(hashes))
	if len(hashes) == 0 {
		slog.Info("nothing to ping")
		return
	}
	if *dryRun {
		slog.Info("dry-run: skipping reference-ping", "would_ping", len(hashes))
		return
	}

	// This job's entire purpose is authenticating to image_service and
	// pinging. Unlike cmd/galgame (which degrades to "no image upload"),
	// missing credentials here means banners silently rot — fail loudly
	// so the external scheduler's alerting catches the misconfig.
	if cfg.ImageClient.ClientID == "" || cfg.ImageClient.ClientSecret == "" {
		slog.Error("image client not configured (KUN_IMAGE_CLIENT_ID / KUN_IMAGE_CLIENT_SECRET) — refusing to run; banners would rot")
		os.Exit(1)
	}
	cli := imageclient.New(imageclient.Config{
		BaseURL:      cfg.ImageClient.BaseURL,
		CDNBase:      cfg.ImageService.CDNBase,
		ClientID:     cfg.ImageClient.ClientID,
		ClientSecret: cfg.ImageClient.ClientSecret,
	})

	var (
		totalUpdated  int64
		totalNotFound int
		batchErrors   int
		sampleMissing []string
	)
	for _, b := range chunk(hashes, *batch) {
		res, err := cli.ReferencePing(ctx, b)
		if err != nil {
			batchErrors++
			slog.Error("reference-ping batch failed", "size", len(b), "err", err)
			continue
		}
		totalUpdated += res.Updated
		totalNotFound += len(res.NotFound)
		if len(sampleMissing) < 10 && len(res.NotFound) > 0 {
			sampleMissing = append(sampleMissing, res.NotFound...)
			if len(sampleMissing) > 10 {
				sampleMissing = sampleMissing[:10]
			}
		}
	}

	// not_found should trend to ~0. Persistent non-zero means the wiki DB
	// holds banner_image_hash values image_service no longer has (dangling
	// refs) — surface a sample so it can be chased down.
	slog.Info("reference-ping complete",
		"distinct_hashes", len(hashes),
		"updated", totalUpdated,
		"not_found", totalNotFound,
		"batch_errors", batchErrors,
	)
	if totalNotFound > 0 {
		slog.Warn("some banner hashes unknown to image_service (dangling local refs)",
			"not_found", totalNotFound, "sample", sampleMissing)
	}

	// Non-zero exit on transport/auth failures so the external scheduler
	// alerts. not_found alone is data drift, not a run failure.
	if batchErrors > 0 {
		os.Exit(1)
	}
}

// chunk splits src into consecutive slices of at most n elements.
func chunk[T any](src []T, n int) [][]T {
	if n < 1 {
		n = 1
	}
	out := make([][]T, 0, (len(src)+n-1)/n)
	for i := 0; i < len(src); i += n {
		out = append(out, src[i:min(i+n, len(src))])
	}
	return out
}
