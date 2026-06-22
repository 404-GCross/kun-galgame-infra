package repository

import (
	"context"

	"api/internal/platform/artifact/model"

	"gorm.io/gorm"
)

// StatsRepository runs aggregation queries over the artifacts table for the
// admin /stats endpoint.
type StatsRepository struct {
	db *gorm.DB
}

func NewStatsRepository(db *gorm.DB) *StatsRepository {
	return &StatsRepository{db: db}
}

// ArtifactStatsResult is the aggregate shape served by GET /admin/artifact/stats.
type ArtifactStatsResult struct {
	TotalCount  int64                        `json:"total_count"`  // ready artifacts (not soft-deleted)
	TotalBytes  int64                        `json:"total_bytes"`  // sum file_size of ready
	Uploading   int64                        `json:"uploading"`    // status=uploading (orphan candidates)
	Failed      int64                        `json:"failed"`       // status=failed
	SoftDeleted int64                        `json:"soft_deleted"` // deleted_at set, pending GC
	BySite      map[string]ArtifactSiteStats `json:"by_site,omitempty"`
}

// ArtifactSiteStats is the per-site breakdown of ready artifacts.
type ArtifactSiteStats struct {
	Count int64 `json:"count"`
	Bytes int64 `json:"bytes"`
}

// Stats aggregates the artifacts table across all sites (admin mode). The
// default GORM scope excludes soft-deleted rows, so ready/uploading/failed
// counts ignore them; SoftDeleted is counted Unscoped.
func (r *StatsRepository) Stats(ctx context.Context) (*ArtifactStatsResult, error) {
	out := &ArtifactStatsResult{BySite: map[string]ArtifactSiteStats{}}

	var ready struct {
		Count int64
		Bytes int64
	}
	if err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("status = ?", model.StatusReady).
		Select("COUNT(*) AS count, COALESCE(SUM(file_size),0) AS bytes").
		Scan(&ready).Error; err != nil {
		return nil, err
	}
	out.TotalCount = ready.Count
	out.TotalBytes = ready.Bytes

	if err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("status = ?", model.StatusUploading).Count(&out.Uploading).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("status = ?", model.StatusFailed).Count(&out.Failed).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Unscoped().Model(&model.Artifact{}).
		Where("deleted_at IS NOT NULL").Count(&out.SoftDeleted).Error; err != nil {
		return nil, err
	}

	var rows []struct {
		SiteKey string
		Count   int64
		Bytes   int64
	}
	if err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("status = ?", model.StatusReady).
		Select("site_key, COUNT(*) AS count, COALESCE(SUM(file_size),0) AS bytes").
		Group("site_key").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, rw := range rows {
		out.BySite[rw.SiteKey] = ArtifactSiteStats{Count: rw.Count, Bytes: rw.Bytes}
	}
	return out, nil
}
