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

// UserAvatarRefpingOpts is the user-avatar refping configuration.
// Defaults match the sibling galgame refping for operational consistency.
type UserAvatarRefpingOpts struct {
	Batch   int           // hashes per reference-ping request (max 1000)
	Timeout time.Duration // overall run timeout
	DryRun  bool          // collect + log only, no image_service call
}

// DefaultUserAvatarRefpingOpts is what the scheduler uses.
func DefaultUserAvatarRefpingOpts() UserAvatarRefpingOpts {
	return UserAvatarRefpingOpts{Batch: 1000, Timeout: 30 * time.Minute}
}

// RunUserAvatarRefping keeps user avatar images alive in image_service.
//
// Background: image_service GCs hashes whose last_referenced_at has been
// unchanged for >365d (soft-delete) → +30d (physical delete). The CDN read
// path goes straight to R2 and never touches image_service, so a user who
// uploaded an avatar once and never changed it would lose it ~13 months
// later. This job emits a daily reference-ping for every current avatar
// hash to keep last_referenced_at fresh.
//
// Scope: only the CURRENT avatar set (users.avatar_image_hash). There is
// no avatar history table — when a user changes their avatar, the old
// hash is overwritten and intentionally allowed to expire (other sites
// that still reference it via their own user mirrors carry their own
// refping responsibility).
//
// Mirrors RunGalgameImageRefping in structure / error handling so an
// operator who knows one knows the other.
func RunUserAvatarRefping(ctx context.Context, cfg *config.Config, opts UserAvatarRefpingOpts) (Summary, error) {
	if opts.Batch < 1 || opts.Batch > 1000 {
		opts.Batch = 1000 // image_service / SDK reject batches > 1000
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// users.avatar_image_hash lives in the OAuth core DB (cfg.Database),
	// NOT the galgame wiki DB.
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
		// users.avatar_image_hash pointing at a hash image_service no
		// longer knows is a real consistency drift — either the user
		// row outlived its avatar (long-inactive user the GC reached
		// before this job ran for the first time) or someone wrote a
		// fabricated hash directly to the DB.
		summary["sample_missing"] = sampleMissing
		slog.Warn("avatar refping: dangling avatar refs (hashes unknown to image_service)",
			"not_found", totalNotFound, "sample", sampleMissing)
	}
	if batchErrors > 0 {
		return summary, fmt.Errorf("reference-ping had %d failed batch(es)", batchErrors)
	}
	return summary, nil
}

// collectUserAvatarHashes returns the distinct non-empty current avatar
// hash set from users. Single source — no historical / per-site mirrors
// to union in (those are the responsibility of the owning service).
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
