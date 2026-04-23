package repository

import (
	"context"
	"time"

	"api/internal/platform/image/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SiteUsageRepository handles CRUD for image_site_usage.
type SiteUsageRepository struct {
	db *gorm.DB
}

func NewSiteUsageRepository(db *gorm.DB) *SiteUsageRepository {
	return &SiteUsageRepository{db: db}
}

// RecordUpload upserts one row: inserts on first (hash, site) upload,
// increments upload_count and updates last_uploaded_at on subsequent ones.
func (r *SiteUsageRepository) RecordUpload(ctx context.Context, hash, site, uploaderSub, uploaderClient string) error {
	now := time.Now()
	usage := &model.ImageSiteUsage{
		Hash:                hash,
		Site:                site,
		FirstUploaderSub:    uploaderSub,
		FirstUploaderClient: uploaderClient,
		FirstUploadedAt:     now,
		UploadCount:         1,
		LastUploadedAt:      now,
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "hash"}, {Name: "site"}},
			DoUpdates: clause.Assignments(map[string]any{
				"upload_count":     gorm.Expr("image_site_usage.upload_count + 1"),
				"last_uploaded_at": now,
			}),
		}).
		Create(usage).
		Error
}

// SitesForHash returns the list of sites that have recorded usage of hash.
// Used by GET /image/:hash for admin-visible `sites` field.
func (r *SiteUsageRepository) SitesForHash(ctx context.Context, hash string) ([]string, error) {
	var sites []string
	err := r.db.WithContext(ctx).
		Model(&model.ImageSiteUsage{}).
		Where("hash = ?", hash).
		Pluck("site", &sites).
		Error
	return sites, err
}
