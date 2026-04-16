package repository

import (
	"context"
	"strings"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// SeriesRepository handles series data access
type SeriesRepository struct {
	db *gorm.DB
}

// NewSeriesRepository creates a new SeriesRepository
func NewSeriesRepository(db *gorm.DB) *SeriesRepository {
	return &SeriesRepository{db: db}
}

// DB exposes the underlying gorm.DB for transactions
func (r *SeriesRepository) DB() *gorm.DB {
	return r.db
}

// List returns a paginated list of series
func (r *SeriesRepository) List(ctx context.Context, page, limit int) ([]model.GalgameSeries, int64, error) {
	var items []model.GalgameSeries
	var total int64

	r.db.WithContext(ctx).Model(&model.GalgameSeries{}).Count(&total)

	err := r.db.WithContext(ctx).
		Select("galgame_series.*, COALESCE(sc.cnt, 0) AS cnt").
		Preload("Galgame", func(db *gorm.DB) *gorm.DB {
			return db.Where("status != 1").Order("created ASC").Limit(5)
		}).
		Joins("LEFT JOIN (SELECT series_id, COUNT(*) AS cnt FROM galgame WHERE series_id IS NOT NULL GROUP BY series_id) sc ON sc.series_id = galgame_series.id").
		Order("cnt DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error

	return items, total, err
}

// FindByID finds a series by ID with all galgames
func (r *SeriesRepository) FindByID(ctx context.Context, id int) (*model.GalgameSeries, error) {
	var series model.GalgameSeries
	err := r.db.WithContext(ctx).
		Preload("Galgame", func(db *gorm.DB) *gorm.DB {
			return db.Where("status != 1").Order("created ASC")
		}).
		First(&series, id).Error
	return &series, err
}

// Create creates a new series and optionally assigns galgames
func (r *SeriesRepository) Create(ctx context.Context, series *model.GalgameSeries, galgameIDs []int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(series).Error; err != nil {
			return err
		}
		if len(galgameIDs) > 0 {
			return tx.Model(&model.Galgame{}).
				Where("id IN ?", galgameIDs).
				Update("series_id", series.ID).Error
		}
		return nil
	})
}

// Update updates a series and reassigns galgames
func (r *SeriesRepository) Update(ctx context.Context, seriesID int, updates map[string]any, galgameIDs []int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&model.GalgameSeries{}).Where("id = ?", seriesID).Updates(updates).Error; err != nil {
				return err
			}
		}

		if galgameIDs != nil {
			// Remove all galgames from this series
			if err := tx.Model(&model.Galgame{}).
				Where("series_id = ?", seriesID).
				Update("series_id", nil).Error; err != nil {
				return err
			}
			// Assign new galgames
			if len(galgameIDs) > 0 {
				if err := tx.Model(&model.Galgame{}).
					Where("id IN ?", galgameIDs).
					Update("series_id", seriesID).Error; err != nil {
					return err
				}
			}
		}

		return nil
	})
}

// Delete deletes a series (galgames get series_id set to NULL via SetNull)
func (r *SeriesRepository) Delete(ctx context.Context, id int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Clear series_id on galgames first
		if err := tx.Model(&model.Galgame{}).
			Where("series_id = ?", id).
			Update("series_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&model.GalgameSeries{}, id).Error
	})
}

// SearchGalgames searches galgames by keywords for series assignment
func (r *SeriesRepository) SearchGalgames(ctx context.Context, keywords []string) ([]model.Galgame, error) {
	var galgames []model.Galgame

	query := r.db.WithContext(ctx).Model(&model.Galgame{}).Where("status != 1")

	for _, kw := range keywords {
		like := "%" + strings.ToLower(kw) + "%"
		tagSub := r.db.Model(&model.GalgameTagRelation{}).
			Select("galgame_id").
			Joins("JOIN galgame_tag ON galgame_tag.id = galgame_tag_relation.tag_id").
			Where("LOWER(galgame_tag.name) LIKE ?", like)
		aliasSub := r.db.Model(&model.GalgameAlias{}).
			Select("galgame_id").
			Where("LOWER(name) LIKE ?", like)
		query = query.Where(
			"vndb_id LIKE ? OR LOWER(name_en_us) LIKE ? OR LOWER(name_ja_jp) LIKE ? OR LOWER(name_zh_cn) LIKE ? OR LOWER(name_zh_tw) LIKE ? OR id IN (?) OR id IN (?)",
			like, like, like, like, like, tagSub, aliasSub,
		)
	}

	err := query.Limit(20).Find(&galgames).Error
	return galgames, err
}

// FindGalgamesByIDs finds galgames by IDs
func (r *SeriesRepository) FindGalgamesByIDs(ctx context.Context, ids []int) ([]model.Galgame, error) {
	var galgames []model.Galgame
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&galgames).Error
	return galgames, err
}
