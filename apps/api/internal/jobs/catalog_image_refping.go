package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

// CatalogImageRefpingOpts mirrors the galgame refping flags.
type CatalogImageRefpingOpts struct {
	Batch   int           // hashes per reference-ping request (max 1000)
	Timeout time.Duration // overall run timeout
	DryRun  bool          // collect + log only, no image_service call
}

// DefaultCatalogImageRefpingOpts is what the scheduler uses.
func DefaultCatalogImageRefpingOpts() CatalogImageRefpingOpts {
	return CatalogImageRefpingOpts{Batch: 1000, Timeout: 30 * time.Minute}
}

// RunCatalogImageRefping keeps catalog-scope images alive in the image service
// (TTL: >365d unreferenced → soft-deleted, +30d → physical delete). These
// images are set-once, so without this daily ping they only ever get the single
// upload-time TTL touch and vanish ~13 months later — the exact "refping
// site-scope GC fuse" failure that froze 66k galgame images.
//
// The catalog-scope hash universe is four sources (step 54, refs/proj/51 §4):
//  1. catalog_character.image_hash — the BUST: VNDB portrait wave (step 48) and
//     the Getchu bust backfill (refs/proj/167 §10).
//  2. catalog_character.figure_hash — the FULL-BODY figure (refs/proj/167 §11).
//  3. catalog_work_cover.image_hash — bodyless cover backfill (step 53).
//  4. catalog_work_screenshot.image_hash — DLsite screenshot backfill (step 54 for
//     bodyless works, refs/proj/125 for the claimed lane).
//
// Adding an image column anywhere in catalog scope means adding it HERE in the
// same change. Nothing fails when you forget: uploads succeed, the read face
// renders, and the bytes are collected a year later.
//
// For (2) and (3) ALL rows count, INCLUDING those shadowed by a later claim
// (§8.B shadow-never-delete): a shadowed media row's bytes stay in catalog scope
// until an explicit handoff, so a missed shadowed row = GC eats a live image.
// Claim state is deliberately NOT a filter here — a claimed work's BRIDGED
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
		opts.Batch = 1000 // image_service / SDK reject batches > 1000
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	clientCfg := cfg.CatalogImageClient
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		// Soft skip — the catalog portrait wave is not provisioned yet.
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
	// All-not-found with zero transport errors is the signature of a site/client
	// misconfiguration (pinging as the wrong site) — or every portrait already
	// TTL-deleted. Either way 0 kept alive is a failed run worth alerting on.
	if totalUpdated == 0 && len(hashes) > 0 {
		return summary, fmt.Errorf("reference-ping kept 0/%d hashes alive (all not_found) — wrong image client/site (need site=catalog) or images already deleted", len(hashes))
	}
	return summary, nil
}

// collectCatalogRefpingHashes returns the deduped set of every non-empty
// catalog-scope image_hash: LIVE character portraits UNIONed with EVERY
// catalog_work_cover row (step 53) and EVERY catalog_work_screenshot row (step 54).
//
//   - Soft-deleted characters are excluded (their portrait may legitimately age
//     out) — a portrait is referenced iff a live character still points at it.
//   - catalog_work_cover / catalog_work_screenshot are taken in FULL — no
//     claim/shadow filter (§8.B shadow-never-delete): a bodyless media row that a
//     later claim shadowed still owns bytes in the catalog scope, so it MUST keep
//     being pinged. Missing it = GC eats a live image (the 66k-frozen class).
//
// Unlike galgame images (which also live in revision/PR snapshots), catalog
// media has exactly one home row each, so this is the whole referenced universe.
func collectCatalogRefpingHashes(ctx context.Context, db *gorm.DB) ([]string, error) {
	const q = `
SELECT DISTINCT hash FROM (
    SELECT image_hash AS hash FROM catalog_character
    WHERE image_hash IS NOT NULL AND image_hash <> '' AND deleted_at IS NULL
    UNION
    -- Characters carry TWO independent images: the bust (image_hash) and the
    -- full-body figure (figure_hash). Both are catalog-scope bytes with one
    -- home row each, so both must be listed here. A new image column that is
    -- not added to this union is invisible to the keep-alive sweep and its
    -- bytes are collected once the TTL elapses — the failure is silent and
    -- arrives a year late.
    SELECT figure_hash FROM catalog_character
    WHERE figure_hash IS NOT NULL AND figure_hash <> '' AND deleted_at IS NULL
    UNION
    SELECT image_hash FROM catalog_work_cover
    WHERE image_hash IS NOT NULL AND image_hash <> ''
    UNION
    SELECT image_hash FROM catalog_work_screenshot
    WHERE image_hash IS NOT NULL AND image_hash <> ''
) u
`
	var hashes []string
	if err := db.WithContext(ctx).Raw(q).Scan(&hashes).Error; err != nil {
		return nil, err
	}
	return hashes, nil
}
