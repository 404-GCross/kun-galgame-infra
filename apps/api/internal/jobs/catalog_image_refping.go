package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/imagerefs"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

type CatalogImageRefpingOpts struct {
	Batch   int
	Timeout time.Duration
	DryRun  bool
}

func DefaultCatalogImageRefpingOpts() CatalogImageRefpingOpts {
	return CatalogImageRefpingOpts{Batch: 1000, Timeout: 30 * time.Minute}
}

// RunCatalogImageRefping keeps catalog-scope images alive in the image service
// (TTL: >365d unreferenced → soft-deleted, +30d → physical delete). These
// images are set-once, so without this daily ping they only ever get the single
// upload-time TTL touch and vanish ~13 months later — the exact "refping
// site-scope GC fuse" failure that froze 66k galgame images.
//
// The catalog-scope hash universe is the imagerefs registry
// (internal/platform/catalog/imagerefs) — six image-bearing columns, and the
// one place a seventh gets added. Read its package doc before changing what
// this sweep pings.
//
// Claim state is deliberately NOT a filter — a claimed work's BRIDGED
// covers/screenshots live in the galgame_wiki scope and are pinged by the
// SEPARATE galgame-image-refping, but its NATIVE catalog_work_screenshot rows
// (the DLsite claimed lane) are catalog-scope bytes owned by this sweep — byte
// discipline §4.
//
// Reference-ping is SITE-SCOPED, so this MUST authenticate as the catalog image
// client (site_key "catalog"); any other identity 404s every hash and the images rot.
//
// Not-yet-provisioned tolerance: until the catalog image client + env are wired,
// CatalogImageClient is empty. Rather than fail the scheduled run daily (alert
// spam before launch), an unconfigured client is a soft skip (no error). A
// CONFIGURED-but-wrong client still trips the all-not-found guard below.
func RunCatalogImageRefping(ctx context.Context, cfg *config.Config, opts CatalogImageRefpingOpts) (Summary, error) {
	if opts.Batch < 1 || opts.Batch > 1000 {
		opts.Batch = 1000
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	clientCfg := cfg.CatalogImageClient
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		slog.Warn("catalog-image-refping: catalog image client not configured — skipping (set KUN_CATALOG_IMAGE_CLIENT_ID/SECRET once the portrait backfill is live)")
		return Summary{"skipped": "catalog image client not configured"}, nil
	}

	db, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	defer db.Close()

	hashes, err := collectCatalogRefpingHashes(ctx, db.DB())
	if err != nil {
		return nil, fmt.Errorf("collect catalog image hashes: %w", err)
	}
	slog.Info("catalog-image-refping: collected catalog-scope hashes", "count", len(hashes))
	if len(hashes) == 0 {
		return Summary{"distinct_hashes": 0, "note": "nothing to ping"}, nil
	}
	if opts.DryRun {
		return Summary{"dry_run": true, "would_ping": len(hashes)}, nil
	}

	slog.Info("catalog-image-refping: image client selected", "client_id", clientCfg.ClientID)
	cli := imageclient.New(imageclient.Config{
		BaseURL:      clientCfg.BaseURL,
		CDNBase:      cfg.ImageService.CDNBase,
		ClientID:     clientCfg.ClientID,
		ClientSecret: clientCfg.ClientSecret,
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
			slog.Error("catalog-image-refping: batch failed", "size", len(b), "err", err)
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
		slog.Warn("catalog-image-refping: dangling local refs (hashes unknown to image_service)",
			"not_found", totalNotFound, "sample", sampleMissing)
	}
	if batchErrors > 0 {
		return summary, fmt.Errorf("reference-ping had %d failed batch(es)", batchErrors)
	}
	if totalUpdated == 0 && len(hashes) > 0 {
		return summary, fmt.Errorf("reference-ping kept 0/%d hashes alive (all not_found) — wrong image client/site (need site=catalog) or images already deleted", len(hashes))
	}
	return summary, nil
}

func collectCatalogRefpingHashes(ctx context.Context, db *gorm.DB) ([]string, error) {
	return imagerefs.DistinctHashes(ctx, db)
}
