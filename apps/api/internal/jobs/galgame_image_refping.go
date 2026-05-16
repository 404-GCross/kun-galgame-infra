package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/imageclient"
)

// GalgameImageRefpingOpts mirrors the original cmd flags. Defaults match
// the old flag defaults so CLI and scheduler behave identically.
type GalgameImageRefpingOpts struct {
	Batch   int           // hashes per reference-ping request (max 1000)
	Timeout time.Duration // overall run timeout
	DryRun  bool          // collect + log only, no image_service call
}

// DefaultGalgameImageRefpingOpts is what the scheduler uses.
func DefaultGalgameImageRefpingOpts() GalgameImageRefpingOpts {
	return GalgameImageRefpingOpts{Batch: 1000, Timeout: 30 * time.Minute}
}

// RunGalgameImageRefping keeps galgame wiki banner images alive in
// image_service (TTL: >365d unreferenced → soft-deleted, +30d → physical
// delete). galgame banners are set-once, so without this daily ping they
// only ever get the single upload-time TTL touch and vanish ~13 months
// later. Body is the original cmd/galgame-image-refping logic, returning
// a Summary / error instead of os.Exit (single source of truth).
func RunGalgameImageRefping(ctx context.Context, cfg *config.Config, opts GalgameImageRefpingOpts) (Summary, error) {
	if opts.Batch < 1 || opts.Batch > 1000 {
		opts.Batch = 1000 // image_service / SDK reject batches > 1000
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	defer db.Close()

	// galgame.banner_image_hash is the authoritative reference. Banner is
	// the only image_service-backed field on the wiki side; avatars etc.
	// live on the OAuth service and are pinged by that caller separately.
	var hashes []string
	if err := db.DB().WithContext(ctx).
		Raw(`SELECT DISTINCT banner_image_hash
		     FROM galgame
		     WHERE banner_image_hash IS NOT NULL AND banner_image_hash <> ''`).
		Scan(&hashes).Error; err != nil {
		return nil, fmt.Errorf("collect banner hashes: %w", err)
	}

	slog.Info("refping: collected banner hashes", "count", len(hashes))
	if len(hashes) == 0 {
		return Summary{"distinct_hashes": 0, "note": "nothing to ping"}, nil
	}
	if opts.DryRun {
		return Summary{"dry_run": true, "would_ping": len(hashes)}, nil
	}

	// This job's entire purpose is authenticating to image_service and
	// pinging. Missing credentials means banners silently rot — fail
	// loudly so the run is recorded failed and surfaces in alerting.
	if cfg.ImageClient.ClientID == "" || cfg.ImageClient.ClientSecret == "" {
		return nil, fmt.Errorf("image client not configured (KUN_IMAGE_CLIENT_ID / KUN_IMAGE_CLIENT_SECRET); refusing to run, banners would rot")
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
	for _, b := range chunk(hashes, opts.Batch) {
		res, err := cli.ReferencePing(ctx, b)
		if err != nil {
			batchErrors++
			slog.Error("refping: batch failed", "size", len(b), "err", err)
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

	summary := Summary{
		"distinct_hashes": len(hashes),
		"updated":         totalUpdated,
		"not_found":       totalNotFound,
		"batch_errors":    batchErrors,
	}
	if totalNotFound > 0 {
		summary["sample_missing"] = sampleMissing
		slog.Warn("refping: dangling local refs (hashes unknown to image_service)",
			"not_found", totalNotFound, "sample", sampleMissing)
	}
	// Transport/auth failures → error (run recorded failed → alert).
	// not_found alone is data drift, not a run failure.
	if batchErrors > 0 {
		return summary, fmt.Errorf("reference-ping had %d failed batch(es)", batchErrors)
	}
	return summary, nil
}
