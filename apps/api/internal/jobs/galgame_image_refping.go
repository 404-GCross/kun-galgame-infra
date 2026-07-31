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

// RunGalgameImageRefping keeps the image_service bytes that only the EDIT
// HISTORY still references alive (TTL: >365d unreferenced → soft-deleted, +30d
// → physical delete). Cover/screenshot bytes are set-once, so without a daily
// ping they get the single upload-time TTL touch and vanish ~13 months later.
//
// Wave 161 removed this job's WIKI LANE — the scan over galgame_cover /
// galgame_screenshot / galgame_revision / galgame_pr — together with the tables
// it read. The surviving subject is the engine lane: image hashes that appear
// ONLY inside a historical edit snapshot or an open proposal, which no live
// table references and which a revert or a diff render must therefore still be
// able to resolve.
//
// It keeps pinging as the galgame_wiki image client and that is deliberate: the
// bytes in question were uploaded under that site, and reference-ping is
// site-scoped. The site KEY itself is never touched by this wave.
func RunGalgameImageRefping(ctx context.Context, cfg *config.Config, opts GalgameImageRefpingOpts) (Summary, error) {
	if opts.Batch < 1 || opts.Batch > 1000 {
		opts.Batch = 1000 // image_service / SDK reject batches > 1000
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// The engine tables (edit_revision / edit_proposal) live on the CATALOG
	// pool. Since the wiki lane retired, this is the only pool the job opens —
	// cfg.GalgameDatabase is no longer read here.
	editDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		return nil, fmt.Errorf("catalog db connect: %w", err)
	}
	defer editDB.Close()

	// The hash universe: every image_hash that has ever appeared in an
	// edit_revision snapshot, an edit_proposal patch, or an archived
	// legacy_meta snapshot. Historical revert and diff must not resurrect a
	// hash image_service has TTL-deleted, and an open proposal's proposed
	// images must survive until it is decided.
	//
	// The four wiki sources this list used to open with (current covers,
	// current screenshots, galgame_revision snapshots, galgame_pr snapshots)
	// retired with their tables at wave 161. The LIVE cover/screenshot rows
	// they protected are now catalog_work_cover / catalog_work_screenshot,
	// which catalog-image-refping walks under site=catalog once wikirescue
	// step o has adopted the byte ownership — that adoption is the same-window
	// precondition for this deletion, not an optional follow-up.
	//
	// Implementation lives in collectEditRefpingHashes so it can be tested
	// against a real DB without an image_service mock.
	hashes, err := collectEditRefpingHashes(ctx, editDB.DB())
	if err != nil {
		return nil, fmt.Errorf("collect edit_* refping hashes: %w", err)
	}

	slog.Info("refping: collected edit-history hashes", "count", len(hashes))
	if len(hashes) == 0 {
		return Summary{"distinct_hashes": 0, "note": "nothing to ping"}, nil
	}
	if opts.DryRun {
		return Summary{"dry_run": true, "would_ping": len(hashes)}, nil
	}

	// reference-ping is SITE-SCOPED: a client can only keep alive hashes its
	// OWN site uploaded. galgame banners/covers live under site=galgame_wiki,
	// so this job MUST authenticate as the galgame_wiki client. In the oauth
	// container (where the scheduler runs) cfg.ImageClient is the *account*
	// client — pinging with it 404s every hash and banners rot. Prefer the
	// dedicated GalgameImageClient; fall back to ImageClient (correct in the
	// galgame container, where ImageClient already == galgame_wiki).
	clientCfg := cfg.ImageClient
	usingDedicated := false
	if cfg.GalgameImageClient.ClientID != "" && cfg.GalgameImageClient.ClientSecret != "" {
		clientCfg = cfg.GalgameImageClient
		usingDedicated = true
	}
	// Missing credentials means banners silently rot — fail loudly so the run
	// is recorded failed and surfaces in alerting.
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		return nil, fmt.Errorf("galgame image client not configured (set KUN_GALGAME_IMAGE_CLIENT_ID/SECRET on the oauth container, or KUN_IMAGE_CLIENT_ID/SECRET); refusing to run, banners would rot")
	}
	slog.Info("refping: image client selected",
		"dedicated_galgame_client", usingDedicated, "client_id", clientCfg.ClientID)
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
	// All-not-found with zero transport errors is the exact signature of a
	// site/client misconfiguration (this job pinging as the wrong site, e.g.
	// account instead of galgame_wiki) — or every image already TTL-deleted.
	// Either way it must NOT pass silently: the job's whole purpose is keeping
	// banners alive, so 0 kept alive is a failed run worth alerting on.
	if totalUpdated == 0 && len(hashes) > 0 {
		return summary, fmt.Errorf("reference-ping kept 0/%d hashes alive (all not_found) — wrong image client/site (need site=galgame_wiki) or images already deleted", len(hashes))
	}
	return summary, nil
}

// collectEditRefpingHashes walks the editing-engine tables (catalog pool) for
// image references:
//
//  1. edit_revision.snapshot — the covers / screenshots field keys
//  2. edit_proposal.patch — same keys (an open proposal's proposed images must
//     survive until it is decided)
//  3. edit_proposal.legacy_meta->'snapshot' — the archived old-wire snapshot
//     of migrated merged PRs
//
// It matches BOTH key spellings, and that is not belt-and-braces. Wave 161's
// rekey rewrites every galgame.game row in these tables to catalog.work with
// the catalog field keys; a galgame.game-only filter would return the empty set
// the moment that migration lands, and this job's own zero-guard would turn a
// successful no-op into a daily failed run with an alert attached. Matching
// both spellings means the same rows keep being found on either side of the
// rekey, and the pre-rekey spelling stops matching anything by itself.
func collectEditRefpingHashes(ctx context.Context, db *gorm.DB) ([]string, error) {
	const q = `
WITH all_hashes AS (
    SELECT (jsonb_array_elements(r.snapshot->k))->>'image_hash' AS hash
        FROM edit_revision r, LATERAL unnest(ARRAY[
            'galgame.game.covers', 'galgame.game.screenshots',
            'catalog.work.covers', 'catalog.work.screenshots']) AS k
        WHERE r.entity_type IN ('galgame.game', 'catalog.work')
          AND jsonb_typeof(r.snapshot->k) = 'array'
    UNION
    SELECT (jsonb_array_elements(p.patch->k))->>'image_hash'
        FROM edit_proposal p, LATERAL unnest(ARRAY[
            'galgame.game.covers', 'galgame.game.screenshots',
            'catalog.work.covers', 'catalog.work.screenshots']) AS k
        WHERE p.entity_type IN ('galgame.game', 'catalog.work')
          AND jsonb_typeof(p.patch->k) = 'array'
    UNION
    -- legacy_meta is the archived OLD-wire snapshot, whose keys are bare
    -- ('covers' / 'screenshots') and are not rewritten by the rekey.
    SELECT (jsonb_array_elements(p.legacy_meta->'snapshot'->k))->>'image_hash'
        FROM edit_proposal p, LATERAL unnest(ARRAY['covers', 'screenshots']) AS k
        WHERE p.entity_type IN ('galgame.game', 'catalog.work')
          AND jsonb_typeof(p.legacy_meta->'snapshot'->k) = 'array'
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
