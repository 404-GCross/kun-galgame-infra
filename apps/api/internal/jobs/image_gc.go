package jobs

import (
	"context"
	"fmt"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/image/repository"
	"api/internal/platform/image/service"
	"api/internal/platform/image/storage"
	"api/pkg/config"
)

// ImageGCOpts mirrors the original cmd/image-gc flags; defaults match.
type ImageGCOpts struct {
	ColdDays  int  // days since last_referenced_at to report as cold-storage candidates
	SoftDays  int  // days since last_referenced_at to soft-delete at
	HardDays  int  // days since deleted_at to hard-delete at
	DryRun    bool // log only, do not mutate
	MaxPerRun int  // max rows per phase per run
}

// DefaultImageGCOpts is what the scheduler uses (= old flag defaults).
func DefaultImageGCOpts() ImageGCOpts {
	return ImageGCOpts{ColdDays: 60, SoftDays: 365, HardDays: 30, MaxPerRun: 10000}
}

// RunImageGC runs the image TTL lifecycle (cold-candidate report →
// soft-delete >SoftDays → hard-delete soft-deleted >HardDays). Body is
// the original cmd/image-gc logic, returning a Summary instead of
// printing — single source of truth.
func RunImageGC(ctx context.Context, cfg *config.Config, opts ImageGCOpts) (Summary, error) {
	o := DefaultImageGCOpts()
	if opts.ColdDays > 0 {
		o.ColdDays = opts.ColdDays
	}
	if opts.SoftDays > 0 {
		o.SoftDays = opts.SoftDays
	}
	if opts.HardDays > 0 {
		o.HardDays = opts.HardDays
	}
	if opts.MaxPerRun > 0 {
		o.MaxPerRun = opts.MaxPerRun
	}
	o.DryRun = opts.DryRun

	db, err := database.NewPostgresDB(cfg.ImagesDatabase)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	defer db.Close()

	s3, err := storage.NewClient(cfg.ImageS3)
	if err != nil {
		return nil, fmt.Errorf("s3 init: %w", err)
	}

	imgRepo := repository.NewImageRepository(db.DB())
	gc := service.NewGCService(db.DB(), s3, imgRepo)

	summary, err := gc.Run(ctx, service.GCConfig{
		ColdAfter:    time.Duration(o.ColdDays) * 24 * time.Hour,
		SoftDelAfter: time.Duration(o.SoftDays) * 24 * time.Hour,
		HardDelAfter: time.Duration(o.HardDays) * 24 * time.Hour,
		DryRun:       o.DryRun,
		MaxPerRun:    o.MaxPerRun,
	})
	if err != nil {
		return nil, fmt.Errorf("gc run: %w", err)
	}
	return Summary{
		"cold_candidates": summary.ColdCandidates,
		"soft_deleted":    summary.SoftDeleted,
		"hard_deleted":    summary.HardDeleted,
		"errors":          summary.Errors,
		"dry_run":         o.DryRun,
	}, nil
}
