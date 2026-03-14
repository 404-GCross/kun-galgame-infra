package repository

import (
	"context"

	"api/internal/platform/content/model"

	"gorm.io/gorm"
)

// ContentRepository handles content data access
type ContentRepository struct {
	db *gorm.DB
}

// NewContentRepository creates a new ContentRepository
func NewContentRepository(db *gorm.DB) *ContentRepository {
	return &ContentRepository{db: db}
}

// FindByID finds content by ID
func (r *ContentRepository) FindByID(ctx context.Context, id uint) (*model.Content, error) {
	var content model.Content
	if err := r.db.WithContext(ctx).First(&content, id).Error; err != nil {
		return nil, err
	}
	return &content, nil
}

// FindByUUID finds content by UUID
func (r *ContentRepository) FindByUUID(ctx context.Context, uuid string) (*model.Content, error) {
	var content model.Content
	if err := r.db.WithContext(ctx).Where("uuid = ?", uuid).First(&content).Error; err != nil {
		return nil, err
	}
	return &content, nil
}

// Create creates new content
func (r *ContentRepository) Create(ctx context.Context, content *model.Content) error {
	return r.db.WithContext(ctx).Create(content).Error
}

// Update updates content
func (r *ContentRepository) Update(ctx context.Context, content *model.Content) error {
	return r.db.WithContext(ctx).Save(content).Error
}

// Delete soft deletes content
func (r *ContentRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.Content{}, id).Error
}

// List lists content with pagination
func (r *ContentRepository) List(ctx context.Context, siteID uint, offset, limit int) ([]model.Content, int64, error) {
	var contents []model.Content
	var total int64

	q := r.db.WithContext(ctx).Model(&model.Content{})
	if siteID > 0 {
		q = q.Where("site_id = ?", siteID)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := q.Offset(offset).Limit(limit).Order("created_at DESC").Find(&contents).Error; err != nil {
		return nil, 0, err
	}

	return contents, total, nil
}
