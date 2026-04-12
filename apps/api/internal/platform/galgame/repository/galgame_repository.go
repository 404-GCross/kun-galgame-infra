package repository

import (
	"context"
	"strings"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// GalgameRepository handles galgame data access on kun_galgame_wiki
type GalgameRepository struct {
	db *gorm.DB
}

// NewGalgameRepository creates a new GalgameRepository
func NewGalgameRepository(db *gorm.DB) *GalgameRepository {
	return &GalgameRepository{db: db}
}

// DB exposes the underlying gorm.DB for transactions
func (r *GalgameRepository) DB() *gorm.DB {
	return r.db
}

// FindByID finds a galgame by ID with all relations
func (r *GalgameRepository) FindByID(ctx context.Context, id int) (*model.Galgame, error) {
	var galgame model.Galgame
	err := r.db.WithContext(ctx).
		Preload("Alias").
		Preload("Series").
		Preload("Contributor").
		Preload("Link").
		Preload("Tag.Tag").
		Preload("Official.Official").
		Preload("Official.Official.Alias").
		Preload("Engine.Engine").
		First(&galgame, id).Error
	if err != nil {
		return nil, err
	}
	return &galgame, nil
}

// ExistsByVNDBID checks if a galgame with the given VNDB ID exists
func (r *GalgameRepository) ExistsByVNDBID(ctx context.Context, vndbID string) (bool, int, error) {
	var galgame model.Galgame
	err := r.db.WithContext(ctx).
		Select("id").
		Where("vndb_id = ?", vndbID).
		First(&galgame).Error
	if err == gorm.ErrRecordNotFound {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return true, galgame.ID, nil
}

// List returns a paginated list of galgames
func (r *GalgameRepository) List(ctx context.Context, page, limit int, sortField, sortOrder, search string) ([]model.Galgame, int64, error) {
	var items []model.Galgame
	var total int64

	query := r.db.WithContext(ctx).Model(&model.Galgame{}).Where("status != 1")

	if search != "" {
		like := "%" + strings.ToLower(search) + "%"
		query = query.Where(
			"LOWER(name_en_us) LIKE ? OR LOWER(name_ja_jp) LIKE ? OR LOWER(name_zh_cn) LIKE ? OR LOWER(name_zh_tw) LIKE ?",
			like, like, like, like,
		)
	}

	query.Count(&total)

	if sortField == "" {
		sortField = "created"
	}
	if sortOrder == "" {
		sortOrder = "desc"
	}
	order := sortField + " " + sortOrder

	err := query.
		Order(order).
		Offset((page - 1) * limit).
		Limit(limit).
		Preload("Tag.Tag").
		Preload("Official.Official").
		Find(&items).Error

	return items, total, err
}

// Create creates a galgame record
func (r *GalgameRepository) Create(ctx context.Context, galgame *model.Galgame) error {
	return r.db.WithContext(ctx).Create(galgame).Error
}

// Update updates a galgame record
func (r *GalgameRepository) Update(ctx context.Context, galgame *model.Galgame) error {
	return r.db.WithContext(ctx).Save(galgame).Error
}

// IncrementView increments the view count
func (r *GalgameRepository) IncrementView(ctx context.Context, id int) error {
	return r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id = ?", id).
		Update("view", gorm.Expr("view + 1")).Error
}
