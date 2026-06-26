package repository

import (
	"context"
	"time"

	"api/internal/platform/artifact/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ArtifactRepository handles artifact data access.
type ArtifactRepository struct {
	db *gorm.DB
}

// NewArtifactRepository creates a new ArtifactRepository.
func NewArtifactRepository(db *gorm.DB) *ArtifactRepository {
	return &ArtifactRepository{db: db}
}

// Create inserts a new artifact row.
func (r *ArtifactRepository) Create(ctx context.Context, a *model.Artifact) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// FindByUUID finds an artifact by its uuid (excludes soft-deleted).
func (r *ArtifactRepository) FindByUUID(ctx context.Context, uuid string) (*model.Artifact, error) {
	var a model.Artifact
	if err := r.db.WithContext(ctx).Where("uuid = ?", uuid).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// Update saves all fields of an artifact.
func (r *ArtifactRepository) Update(ctx context.Context, a *model.Artifact) error {
	return r.db.WithContext(ctx).Save(a).Error
}

// ListBySite returns a site's artifacts (newest first), with the total count.
func (r *ArtifactRepository) ListBySite(ctx context.Context, site string, offset, limit int) ([]model.Artifact, int64, error) {
	var (
		items []model.Artifact
		total int64
	)
	q := r.db.WithContext(ctx).Model(&model.Artifact{}).Where("site_key = ?", site)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Offset(offset).Limit(limit).Order("created_at DESC").Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// SoftDeleteByUUID soft-deletes an artifact scoped to a site. Returns false if
// no matching live row existed (unknown uuid or other site).
func (r *ArtifactRepository) SoftDeleteByUUID(ctx context.Context, uuid, site string) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("uuid = ? AND site_key = ?", uuid, site).
		Delete(&model.Artifact{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// SaveManifest upserts the (1:1) manifest for an artifact.
func (r *ArtifactRepository) SaveManifest(ctx context.Context, m *model.Manifest) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "artifact_id"}},
		UpdateAll: true,
	}).Create(m).Error
}

// Touch bumps updated_at on an artifact without rewriting its other columns.
// Called when an upload is resumed so the GC orphan sweep — which keys off
// updated_at (see FindOrphans) — won't reap an upload that's actively being
// continued, only one genuinely abandoned for OrphanTTL.
func (r *ArtifactRepository) Touch(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).
		Model(&model.Artifact{}).
		Where("id = ?", id).
		Update("updated_at", time.Now()).Error
}

// FindOrphans returns live artifacts stuck uploading (status=0) untouched since
// before the given time — Init'd (or last resumed) but never Completed. Keyed on
// updated_at, NOT created_at, so a long upload that's still being resumed (which
// Touches updated_at) isn't reclaimed mid-flight; only a genuinely abandoned one
// (no progress for OrphanTTL) is. Used by the GC job.
func (r *ArtifactRepository) FindOrphans(ctx context.Context, before time.Time, limit int) ([]model.Artifact, error) {
	var items []model.Artifact
	err := r.db.WithContext(ctx).
		Where("status = ? AND updated_at < ?", model.StatusUploading, before).
		Limit(limit).
		Find(&items).Error
	return items, err
}

// FindExpiredSoftDeleted returns soft-deleted artifacts whose deleted_at is
// older than the given time — ready for physical removal. Used by the GC job.
func (r *ArtifactRepository) FindExpiredSoftDeleted(ctx context.Context, before time.Time, limit int) ([]model.Artifact, error) {
	var items []model.Artifact
	err := r.db.WithContext(ctx).Unscoped().
		Where("deleted_at IS NOT NULL AND deleted_at < ?", before).
		Limit(limit).
		Find(&items).Error
	return items, err
}

// HardDelete physically removes an artifact row (and cascades its manifest).
func (r *ArtifactRepository) HardDelete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Unscoped().Delete(&model.Artifact{}, id).Error
}
