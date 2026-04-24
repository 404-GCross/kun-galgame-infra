package repository

import (
	"context"
	"time"

	"api/internal/platform/image/model"

	"gorm.io/gorm"
)

// StatsRepository runs aggregation queries over images + image_site_usage
// for the /stats and /admin/stats endpoints.
type StatsRepository struct {
	db *gorm.DB
}

func NewStatsRepository(db *gorm.DB) *StatsRepository {
	return &StatsRepository{db: db}
}

// StatsResult is the aggregate shape served by GET /stats.
type StatsResult struct {
	UploadCount        int64           `json:"upload_count"`        // total site-scoped uploads (sum of upload_count)
	UniqueImages       int64           `json:"unique_images"`       // distinct hashes the site has used
	DeduplicatedCount  int64           `json:"deduplicated_count"`  // upload_count - unique_images
	TotalBytes         int64           `json:"total_bytes"`         // sum of referenced images' size_bytes
	BySite             map[string]SiteStats `json:"by_site,omitempty"`
	ReviewPending      int64           `json:"review_pending"`
	ReviewRejected     int64           `json:"review_rejected"`
}

// SiteStats is the per-site breakdown used by admin view.
type SiteStats struct {
	Count  int64 `json:"count"`  // upload_count sum
	Unique int64 `json:"unique"` // distinct hashes
}

// ScopeFilter narrows stats queries to a single site or a time range.
type ScopeFilter struct {
	Site string    // if empty, not filtered
	From time.Time // zero → no lower bound
	To   time.Time // zero → no upper bound
}

// Stats aggregates across site_usage + images tables.
// If f.Site is "", aggregates across all sites (admin mode).
func (r *StatsRepository) Stats(ctx context.Context, f ScopeFilter) (*StatsResult, error) {
	out := &StatsResult{BySite: map[string]SiteStats{}}

	usageQ := r.db.WithContext(ctx).Model(&model.ImageSiteUsage{})
	if f.Site != "" {
		usageQ = usageQ.Where("site = ?", f.Site)
	}
	if !f.From.IsZero() {
		usageQ = usageQ.Where("first_uploaded_at >= ?", f.From)
	}
	if !f.To.IsZero() {
		usageQ = usageQ.Where("first_uploaded_at <= ?", f.To)
	}

	// SELECT SUM(upload_count), COUNT(*) [as unique]
	type usageAgg struct {
		TotalCount  int64
		UniqueCount int64
	}
	var agg usageAgg
	if err := usageQ.
		Select("COALESCE(SUM(upload_count),0) AS total_count, COUNT(*) AS unique_count").
		Scan(&agg).Error; err != nil {
		return nil, err
	}
	out.UploadCount = agg.TotalCount
	out.UniqueImages = agg.UniqueCount
	out.DeduplicatedCount = agg.TotalCount - agg.UniqueCount

	// Total storage bytes — sum over the DISTINCT hashes referenced by
	// this site (avoids double-counting the same hash used by many sites).
	bytesRow := r.db.WithContext(ctx).
		Model(&model.Image{}).
		Where("deleted_at IS NULL")
	if f.Site != "" {
		bytesRow = bytesRow.Where("hash IN (?)",
			r.db.Model(&model.ImageSiteUsage{}).Select("hash").Where("site = ?", f.Site))
	}
	if err := bytesRow.Select("COALESCE(SUM(size_bytes),0)").Scan(&out.TotalBytes).Error; err != nil {
		return nil, err
	}

	// Review status counts (unfiltered by site — review is global per hash).
	if f.Site == "" {
		if err := r.db.WithContext(ctx).Model(&model.Image{}).
			Where("review_status = ? AND deleted_at IS NULL", model.ReviewPending).
			Count(&out.ReviewPending).Error; err != nil {
			return nil, err
		}
		if err := r.db.WithContext(ctx).Model(&model.Image{}).
			Where("review_status = ? AND deleted_at IS NULL", model.ReviewRejected).
			Count(&out.ReviewRejected).Error; err != nil {
			return nil, err
		}

		// By-site breakdown only in admin (all-sites) mode.
		type row struct {
			Site   string
			Count  int64
			Unique int64
		}
		var rows []row
		if err := r.db.WithContext(ctx).Model(&model.ImageSiteUsage{}).
			Select("site, COALESCE(SUM(upload_count),0) AS count, COUNT(*) AS unique").
			Group("site").
			Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, rw := range rows {
			out.BySite[rw.Site] = SiteStats{Count: rw.Count, Unique: rw.Unique}
		}
	}

	return out, nil
}
