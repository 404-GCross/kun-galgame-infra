package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/news/imagerefs"
	"api/pkg/config"
	"api/pkg/imageclient"
)

type NewsImageRefpingOpts struct {
	Batch   int
	Timeout time.Duration
	DryRun  bool
}

func DefaultNewsImageRefpingOpts() NewsImageRefpingOpts {
	return NewsImageRefpingOpts{Batch: 1000, Timeout: 30 * time.Minute}
}

// RunNewsImageRefping keeps news-scope images alive in the image service
// (unreferenced >365d → soft-deleted, +30d → physical delete). Every image in
// the feed is a copy we made of a partner's picture so the dependency graph
// stays ours; they are written once and never touched again, so without this
// daily ping they carry only the upload-time TTL touch and vanish about
// thirteen months later.
//
// The hash universe is the imagerefs registry — read its package doc before
// changing what this sweep pings.
//
// Reference-ping is SITE-SCOPED: this MUST authenticate as the news image
// client. Any other identity 404s every hash and the bytes rot anyway.
// ⚠️ Never the galgame_wiki client.
func RunNewsImageRefping(ctx context.Context, cfg *config.Config, opts NewsImageRefpingOpts) (Summary, error) {
	if opts.Batch < 1 || opts.Batch > 1000 {
		opts.Batch = 1000
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	clientCfg := cfg.NewsImageClient
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		slog.Warn("news-image-refping: news image client not configured — skipping (set KUN_NEWS_IMAGE_CLIENT_ID/SECRET once the ingestion lane is live)")
		return Summary{"skipped": "news image client not configured"}, nil
	}

	db, err := database.NewPostgresDB(cfg.NewsDatabase)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	defer db.Close()

	hashes, err := imagerefs.DistinctHashes(ctx, db.DB())
	if err != nil {
		return nil, fmt.Errorf("collect news image hashes: %w", err)
	}
	slog.Info("news-image-refping: collected news-scope hashes", "count", len(hashes))
	if len(hashes) == 0 {
		return Summary{"distinct_hashes": 0, "note": "nothing to ping"}, nil
	}
	if opts.DryRun {
		return Summary{"dry_run": true, "would_ping": len(hashes)}, nil
	}

	slog.Info("news-image-refping: image client selected", "client_id", clientCfg.ClientID)
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
			slog.Error("news-image-refping: batch failed", "size", len(b), "err", err)
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
		slog.Warn("news-image-refping: dangling local refs (hashes unknown to image_service)",
			"not_found", totalNotFound, "sample", sampleMissing)
	}
	if batchErrors > 0 {
		return summary, fmt.Errorf("reference-ping had %d failed batch(es)", batchErrors)
	}
	if totalUpdated == 0 {
		return summary, fmt.Errorf("reference-ping kept 0/%d hashes alive (all not_found) — wrong image client/site (need the news site) or images already deleted", len(hashes))
	}
	return summary, nil
}
