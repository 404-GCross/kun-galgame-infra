package repository

import (
	"context"
	"time"

	"api/internal/platform/image/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SiteUsageRepository struct {
	db *gorm.DB
}

func NewSiteUsageRepository(db *gorm.DB) *SiteUsageRepository {
	return &SiteUsageRepository{db: db}
}

func (r *SiteUsageRepository) ExistingHashesForSite(ctx context.Context, site string, hashes []string) ([]string, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	var out []string
	err := r.db.WithContext(ctx).
		Model(&model.ImageSiteUsage{}).
		Where("site = ? AND hash IN ?", site, hashes).
		Where("hash IN (?)", r.db.Model(&model.Image{}).Select("hash").Where("deleted_at IS NULL")).
		Distinct().
		Pluck("hash", &out).Error
	return out, err
}

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

func (r *SiteUsageRepository) SitesForHash(ctx context.Context, hash string) ([]string, error) {
	var sites []string
	err := r.db.WithContext(ctx).
		Model(&model.ImageSiteUsage{}).
		Where("hash = ?", hash).
		Pluck("site", &sites).
		Error
	return sites, err
}
