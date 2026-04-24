package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/platform/image/model"
	"api/internal/platform/image/repository"
	"api/internal/platform/image/storage"

	"gorm.io/gorm"
)

// GCService implements the TTL lifecycle: transitioning images from hot
// to cold storage to soft-deleted to physical deletion based on
// last_referenced_at.
//
// Thresholds (from docs/image_service/02-storage-and-schema.md):
//   - > 60d no ping  → (placeholder) mark for cold storage
//   - > 365d no ping → soft-delete (set deleted_at)
//   - deleted_at + 30d → physical delete (remove S3 objects + DELETE row)
//
// The "mark for cold" step is a placeholder: S3 storage-class transitions
// aren't implemented in V1 (object storage lifecycle rules or an explicit
// CopyObject to IA are options). This GC only logs what would transition.
type GCService struct {
	db      *gorm.DB
	storage *storage.Client
	imgRepo *repository.ImageRepository
}

func NewGCService(db *gorm.DB, s *storage.Client, imgRepo *repository.ImageRepository) *GCService {
	return &GCService{db: db, storage: s, imgRepo: imgRepo}
}

// Config controls the GC thresholds. Defaults match docs.
type GCConfig struct {
	ColdAfter     time.Duration // default 60 * 24h
	SoftDelAfter  time.Duration // default 365 * 24h
	HardDelAfter  time.Duration // default 30 * 24h (after soft-delete)
	DryRun        bool          // if true, log only, do not mutate
	MaxPerRun     int           // max rows per phase per run (throttle)
}

func (c *GCConfig) defaults() {
	if c.ColdAfter == 0 {
		c.ColdAfter = 60 * 24 * time.Hour
	}
	if c.SoftDelAfter == 0 {
		c.SoftDelAfter = 365 * 24 * time.Hour
	}
	if c.HardDelAfter == 0 {
		c.HardDelAfter = 30 * 24 * time.Hour
	}
	if c.MaxPerRun == 0 {
		c.MaxPerRun = 10000
	}
}

// Run executes all three phases in sequence. Returns a summary.
type GCRunSummary struct {
	ColdCandidates int64 `json:"cold_candidates"`
	SoftDeleted    int64 `json:"soft_deleted"`
	HardDeleted    int64 `json:"hard_deleted"`
	Errors         int   `json:"errors"`
}

func (g *GCService) Run(ctx context.Context, cfg GCConfig) (*GCRunSummary, error) {
	cfg.defaults()
	summary := &GCRunSummary{}

	coldThreshold := time.Now().Add(-cfg.ColdAfter)
	softThreshold := time.Now().Add(-cfg.SoftDelAfter)
	hardThreshold := time.Now().Add(-cfg.HardDelAfter)

	// --- Phase 1: cold-storage candidates (log only, no mutation) ---
	if err := g.db.WithContext(ctx).Model(&model.Image{}).
		Where("deleted_at IS NULL AND last_referenced_at < ?", coldThreshold).
		Count(&summary.ColdCandidates).Error; err != nil {
		summary.Errors++
		slog.Error("gc count cold candidates", "err", err)
	}
	slog.Info("gc cold candidates",
		"threshold", coldThreshold.Format(time.RFC3339),
		"count", summary.ColdCandidates,
		"dry_run", cfg.DryRun,
	)

	// --- Phase 2: soft-delete images unreferenced for > 365d ---
	if n, err := g.softDelete(ctx, softThreshold, cfg); err != nil {
		summary.Errors++
	} else {
		summary.SoftDeleted = n
	}

	// --- Phase 3: hard-delete rows soft-deleted > 30d ago ---
	if n, err := g.hardDelete(ctx, hardThreshold, cfg); err != nil {
		summary.Errors++
	} else {
		summary.HardDeleted = n
	}

	return summary, nil
}

func (g *GCService) softDelete(ctx context.Context, threshold time.Time, cfg GCConfig) (int64, error) {
	var rows []model.Image
	if err := g.db.WithContext(ctx).
		Where("deleted_at IS NULL AND last_referenced_at < ?", threshold).
		Limit(cfg.MaxPerRun).
		Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("gc: fetch soft-delete candidates: %w", err)
	}
	if len(rows) == 0 {
		slog.Info("gc soft-delete: nothing to do", "threshold", threshold.Format(time.RFC3339))
		return 0, nil
	}
	slog.Info("gc soft-delete candidates",
		"count", len(rows), "threshold", threshold.Format(time.RFC3339), "dry_run", cfg.DryRun)

	if cfg.DryRun {
		return int64(len(rows)), nil
	}

	now := time.Now()
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if err := g.db.WithContext(ctx).
		Model(&model.Image{}).
		Where("id IN ?", ids).
		Update("deleted_at", now).Error; err != nil {
		return 0, fmt.Errorf("gc: apply soft-delete: %w", err)
	}
	return int64(len(rows)), nil
}

func (g *GCService) hardDelete(ctx context.Context, threshold time.Time, cfg GCConfig) (int64, error) {
	var rows []model.Image
	if err := g.db.WithContext(ctx).Unscoped().
		Where("deleted_at IS NOT NULL AND deleted_at < ?", threshold).
		Limit(cfg.MaxPerRun).
		Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("gc: fetch hard-delete candidates: %w", err)
	}
	if len(rows) == 0 {
		slog.Info("gc hard-delete: nothing to do", "threshold", threshold.Format(time.RFC3339))
		return 0, nil
	}
	slog.Info("gc hard-delete candidates",
		"count", len(rows), "threshold", threshold.Format(time.RFC3339), "dry_run", cfg.DryRun)

	if cfg.DryRun {
		return int64(len(rows)), nil
	}

	deleted := int64(0)
	for _, r := range rows {
		// Delete all objects from S3: main + each variant.
		if err := g.storage.Delete(ctx, r.StorageKey); err != nil {
			slog.Warn("gc: delete main s3 object", "hash", r.Hash, "err", err)
		}
		for _, v := range r.VariantList() {
			key := fmt.Sprintf("%s/%s/%s_%s.%s", r.Hash[:2], r.Hash[2:4], r.Hash, v, r.Ext)
			if err := g.storage.Delete(ctx, key); err != nil {
				slog.Warn("gc: delete variant", "hash", r.Hash, "variant", v, "err", err)
			}
		}

		// Row delete (hard).
		if err := g.db.WithContext(ctx).Unscoped().
			Where("id = ?", r.ID).
			Delete(&model.Image{}).Error; err != nil {
			slog.Error("gc: row hard-delete", "id", r.ID, "err", err)
			continue
		}

		// Orphan site_usage rows for this hash — remove them too, since
		// the image is gone.
		if err := g.db.WithContext(ctx).
			Where("hash = ?", r.Hash).
			Delete(&model.ImageSiteUsage{}).Error; err != nil {
			slog.Warn("gc: delete site_usage", "hash", r.Hash, "err", err)
		}
		deleted++
	}

	return deleted, nil
}
