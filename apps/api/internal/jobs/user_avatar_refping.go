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

type UserAvatarRefpingOpts struct {
	Batch   int
	Timeout time.Duration
	DryRun  bool
}

func DefaultUserAvatarRefpingOpts() UserAvatarRefpingOpts {
	return UserAvatarRefpingOpts{Batch: 1000, Timeout: 30 * time.Minute}
}

func RunUserAvatarRefping(ctx context.Context, cfg *config.Config, opts UserAvatarRefpingOpts) (Summary, error) {
	if opts.Batch < 1 || opts.Batch > 1000 {
		opts.Batch = 1000
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	db, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	defer db.Close()

	hashes, err := collectUserAvatarHashes(ctx, db.DB())
	if err != nil {
		return nil, fmt.Errorf("collect avatar hashes: %w", err)
	}

	slog.Info("avatar refping: collected avatar hashes", "count", len(hashes))
	if len(hashes) == 0 {
		return Summary{"distinct_hashes": 0, "note": "nothing to ping"}, nil
	}
	if opts.DryRun {
		return Summary{"dry_run": true, "would_ping": len(hashes)}, nil
	}

	if cfg.ImageClient.ClientID == "" || cfg.ImageClient.ClientSecret == "" {
		return nil, fmt.Errorf("image client not configured (KUN_IMAGE_CLIENT_ID / KUN_IMAGE_CLIENT_SECRET); refusing to run, avatars would rot")
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
			slog.Error("avatar refping: batch failed", "size", len(b), "err", err)
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
		slog.Warn("avatar refping: dangling avatar refs (hashes unknown to image_service)",
			"not_found", totalNotFound, "sample", sampleMissing)
	}
	if batchErrors > 0 {
		return summary, fmt.Errorf("reference-ping had %d failed batch(es)", batchErrors)
	}
	return summary, nil
}

func collectUserAvatarHashes(ctx context.Context, db *gorm.DB) ([]string, error) {
	const q = `
SELECT DISTINCT avatar_image_hash
FROM users
WHERE avatar_image_hash IS NOT NULL AND avatar_image_hash <> ''
`
	var hashes []string
	if err := db.WithContext(ctx).Raw(q).Scan(&hashes).Error; err != nil {
		return nil, err
	}
	return hashes, nil
}
