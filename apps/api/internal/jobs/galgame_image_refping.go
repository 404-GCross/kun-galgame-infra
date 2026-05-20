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

	// The hash universe we must keep alive in image_service:
	//   1. current galgame_cover.image_hash (the authoritative cover set)
	//   2. current galgame_screenshot.image_hash
	//   3. EVERY image_hash that has ever appeared in a galgame_revision
	//      snapshot — historical revert / diff must not resurrect a hash that
	//      image_service has TTL-deleted
	//   4. EVERY image_hash in a galgame_pr.snapshot — same reasoning for
	//      pending PRs (admin may still merge them and trigger revert paths)
	//
	// (The pre-PR5 banner_image_hash source was retired together with the
	// column; pinned-cover via galgame_cover is now the sole banner ref.
	// Old snapshot rows that still carry the banner_image_hash jsonb key
	// were patched by migrate-drop-banner-image-hash to embed the value
	// into covers[] before the column drop.)
	//
	// Implementation lives in collectRefpingHashes so it can be tested
	// against a real DB without standing up an image_service mock.
	hashes, err := collectRefpingHashes(ctx, db.DB())
	if err != nil {
		return nil, fmt.Errorf("collect refping hashes: %w", err)
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

// collectRefpingHashes returns the deduped union of every image hash that
// the galgame wiki currently OR historically references. Four sources
// (see RunGalgameImageRefping comment for the rationale):
//
//   1. galgame_cover.image_hash
//   2. galgame_screenshot.image_hash
//   3. galgame_revision.snapshot — jsonb walk of covers[].image_hash +
//      screenshots[].image_hash
//   4. galgame_pr.snapshot — same as #3
//
// All NULL / empty values are filtered out. Postgres jsonb operators
// `?` (top-key exists), `->` and `jsonb_array_elements` do the heavy
// lifting — there is no Go-side jsonb walk.
func collectRefpingHashes(ctx context.Context, db *gorm.DB) ([]string, error) {
	const q = `
WITH all_hashes AS (
    -- (1) current cover set
    SELECT image_hash AS hash FROM galgame_cover
    UNION
    -- (2) current screenshot set
    SELECT image_hash FROM galgame_screenshot
    UNION
    -- (3a) historical revision: every cover entry
    SELECT (jsonb_array_elements(snapshot->'covers'))->>'image_hash'
        FROM galgame_revision
        WHERE jsonb_typeof(snapshot->'covers') = 'array'
    UNION
    -- (3b) historical revision: every screenshot entry
    SELECT (jsonb_array_elements(snapshot->'screenshots'))->>'image_hash'
        FROM galgame_revision
        WHERE jsonb_typeof(snapshot->'screenshots') = 'array'
    UNION
    -- (4a) PR snapshots: cover entries
    SELECT (jsonb_array_elements(snapshot->'covers'))->>'image_hash'
        FROM galgame_pr
        WHERE jsonb_typeof(snapshot->'covers') = 'array'
    UNION
    -- (4b) PR snapshots: screenshot entries
    SELECT (jsonb_array_elements(snapshot->'screenshots'))->>'image_hash'
        FROM galgame_pr
        WHERE jsonb_typeof(snapshot->'screenshots') = 'array'
)
SELECT DISTINCT hash FROM all_hashes
WHERE hash IS NOT NULL AND hash <> ''
`
	var hashes []string
	if err := db.WithContext(ctx).Raw(q).Scan(&hashes).Error; err != nil {
		return nil, err
	}
	return hashes, nil
}
