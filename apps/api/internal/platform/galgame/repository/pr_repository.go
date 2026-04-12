package repository

import (
	"context"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// PRRepository handles PR data access
type PRRepository struct {
	db *gorm.DB
}

// NewPRRepository creates a new PRRepository
func NewPRRepository(db *gorm.DB) *PRRepository {
	return &PRRepository{db: db}
}

// List returns PRs for a galgame
func (r *PRRepository) List(ctx context.Context, galgameID, page, limit int) ([]model.GalgamePR, int64, error) {
	var items []model.GalgamePR
	var total int64

	query := r.db.WithContext(ctx).Model(&model.GalgamePR{}).Where("galgame_id = ?", galgameID)
	query.Count(&total)

	err := query.
		Order("created DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error

	return items, total, err
}

// FindByID finds a PR by ID
func (r *PRRepository) FindByID(ctx context.Context, id int) (*model.GalgamePR, error) {
	var pr model.GalgamePR
	err := r.db.WithContext(ctx).First(&pr, id).Error
	return &pr, err
}

// Create creates a new PR
func (r *PRRepository) Create(ctx context.Context, pr *model.GalgamePR) error {
	return r.db.WithContext(ctx).Create(pr).Error
}
