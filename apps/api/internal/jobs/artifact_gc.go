package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/artifact/repository"
	"api/internal/platform/artifact/storage"
	"api/pkg/config"
)

// ArtifactGCOpts tunes the artifact lifecycle sweep.
type ArtifactGCOpts struct {
	OrphanTTL     time.Duration // status=uploading older than this → abort+delete
	SoftDeleteTTL time.Duration // deleted_at older than this → physical delete
	MaxPerRun     int           // max rows per phase per run
	DryRun        bool
}

// DefaultArtifactGCOpts derives defaults from config (with hard fallbacks).
func DefaultArtifactGCOpts(cfg *config.Config) ArtifactGCOpts {
	o := ArtifactGCOpts{
		OrphanTTL:     cfg.ArtifactService.OrphanTTL,
		SoftDeleteTTL: cfg.ArtifactService.SoftDeleteTTL,
		MaxPerRun:     10000,
	}
	if o.OrphanTTL <= 0 {
		o.OrphanTTL = 24 * time.Hour
	}
	if o.SoftDeleteTTL <= 0 {
		o.SoftDeleteTTL = 7 * 24 * time.Hour
	}
	return o
}

// RunArtifactGC reclaims storage in two phases, using the least-privilege
// cleanup key (DeleteObject only):
//   - orphans: status=uploading rows past OrphanTTL (Init'd, never Completed) →
//     abort any multipart upload + delete object + drop the row
//   - expired soft-deletes: deleted_at past SoftDeleteTTL → delete object + drop row
func RunArtifactGC(ctx context.Context, cfg *config.Config, opts ArtifactGCOpts) (Summary, error) {
	// No-op when the artifact service isn't configured yet (e.g. prod oauth
	// before B2 credentials are set). This job is registered in the oauth
	// process; without this guard it would error daily trying to reach an
	// absent kun_artifacts DB / unconfigured S3. Storage creds are the signal
	// that artifact is live.
	if cfg.ArtifactS3.AccessKeyID == "" {
		return Summary{"skipped": "artifact storage not configured"}, nil
	}

	if opts.MaxPerRun <= 0 {
		opts.MaxPerRun = 10000
	}
	if opts.OrphanTTL <= 0 {
		opts.OrphanTTL = 24 * time.Hour
	}
	if opts.SoftDeleteTTL <= 0 {
		opts.SoftDeleteTTL = 7 * 24 * time.Hour
	}

	db, err := database.NewPostgresDB(cfg.ArtifactsDatabase)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	defer db.Close()

	// Cleanup-scoped S3 client (DeleteObject only).
	store, err := storage.NewClient(cfg.ArtifactCleanupS3())
	if err != nil {
		return nil, fmt.Errorf("s3 init: %w", err)
	}

	repo := repository.NewArtifactRepository(db.DB())
	now := time.Now()

	var orphans, softDeleted, errCount int

	// Phase 1: orphaned uploads.
	orphanRows, err := repo.FindOrphans(ctx, now.Add(-opts.OrphanTTL), opts.MaxPerRun)
	if err != nil {
		return nil, fmt.Errorf("find orphans: %w", err)
	}
	for i := range orphanRows {
		if err := ctx.Err(); err != nil {
			return partialSummary(orphans, softDeleted, errCount, opts.DryRun), err
		}
		a := &orphanRows[i]
		if opts.DryRun {
			orphans++
			continue
		}
		if a.UploadID != "" {
			if err := store.AbortMultipart(ctx, a.FileKey, a.UploadID); err != nil {
				slog.Warn("artifact-gc: abort multipart", "uuid", a.UUID, "err", err)
			}
		}
		if err := store.Delete(ctx, a.FileKey); err != nil {
			slog.Warn("artifact-gc: delete orphan object", "uuid", a.UUID, "err", err)
		}
		if err := repo.HardDelete(ctx, a.ID); err != nil {
			slog.Error("artifact-gc: drop orphan row", "uuid", a.UUID, "err", err)
			errCount++
			continue
		}
		orphans++
	}

	// Phase 2: expired soft-deletes.
	delRows, err := repo.FindExpiredSoftDeleted(ctx, now.Add(-opts.SoftDeleteTTL), opts.MaxPerRun)
	if err != nil {
		return nil, fmt.Errorf("find soft-deleted: %w", err)
	}
	for i := range delRows {
		if err := ctx.Err(); err != nil {
			return partialSummary(orphans, softDeleted, errCount, opts.DryRun), err
		}
		a := &delRows[i]
		if opts.DryRun {
			softDeleted++
			continue
		}
		if err := store.Delete(ctx, a.FileKey); err != nil {
			slog.Warn("artifact-gc: delete soft-deleted object", "uuid", a.UUID, "err", err)
		}
		if err := repo.HardDelete(ctx, a.ID); err != nil {
			slog.Error("artifact-gc: drop soft-deleted row", "uuid", a.UUID, "err", err)
			errCount++
			continue
		}
		softDeleted++
	}

	return partialSummary(orphans, softDeleted, errCount, opts.DryRun), nil
}

func partialSummary(orphans, softDeleted, errCount int, dryRun bool) Summary {
	return Summary{
		"orphans_reclaimed":   orphans,
		"soft_deleted_purged": softDeleted,
		"errors":              errCount,
		"dry_run":             dryRun,
	}
}
