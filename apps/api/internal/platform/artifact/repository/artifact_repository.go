package repository

import (
	"context"

	"api/internal/platform/artifact/model"

	"gorm.io/gorm"
)

// ArtifactRepository handles artifact data access
type ArtifactRepository struct {
	db *gorm.DB
}

// NewArtifactRepository creates a new ArtifactRepository
func NewArtifactRepository(db *gorm.DB) *ArtifactRepository {
	return &ArtifactRepository{db: db}
}

// FindByID finds an artifact by ID
func (r *ArtifactRepository) FindByID(ctx context.Context, id uint) (*model.Artifact, error) {
	var artifact model.Artifact
	if err := r.db.WithContext(ctx).First(&artifact, id).Error; err != nil {
		return nil, err
	}
	return &artifact, nil
}

// FindByUUID finds an artifact by UUID
func (r *ArtifactRepository) FindByUUID(ctx context.Context, uuid string) (*model.Artifact, error) {
	var artifact model.Artifact
	if err := r.db.WithContext(ctx).Where("uuid = ?", uuid).First(&artifact).Error; err != nil {
		return nil, err
	}
	return &artifact, nil
}

// Create creates a new artifact
func (r *ArtifactRepository) Create(ctx context.Context, artifact *model.Artifact) error {
	return r.db.WithContext(ctx).Create(artifact).Error
}

// Update updates an artifact
func (r *ArtifactRepository) Update(ctx context.Context, artifact *model.Artifact) error {
	return r.db.WithContext(ctx).Save(artifact).Error
}

// Delete soft deletes an artifact
func (r *ArtifactRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.Artifact{}, id).Error
}

// List lists artifacts with pagination
func (r *ArtifactRepository) List(ctx context.Context, offset, limit int) ([]model.Artifact, int64, error) {
	var artifacts []model.Artifact
	var total int64

	if err := r.db.WithContext(ctx).Model(&model.Artifact{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := r.db.WithContext(ctx).Offset(offset).Limit(limit).Order("created_at DESC").Find(&artifacts).Error; err != nil {
		return nil, 0, err
	}

	return artifacts, total, nil
}

// UpdateStatus updates artifact status
func (r *ArtifactRepository) UpdateStatus(ctx context.Context, id uint, status int) error {
	return r.db.WithContext(ctx).Model(&model.Artifact{}).Where("id = ?", id).Update("status", status).Error
}
