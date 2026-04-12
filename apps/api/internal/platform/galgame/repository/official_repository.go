package repository

import (
	"context"
	"strings"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// OfficialRepository handles official data access
type OfficialRepository struct {
	db *gorm.DB
}

// NewOfficialRepository creates a new OfficialRepository
func NewOfficialRepository(db *gorm.DB) *OfficialRepository {
	return &OfficialRepository{db: db}
}

// List returns a paginated list of officials with galgame counts
func (r *OfficialRepository) List(ctx context.Context, page, limit int) ([]model.GalgameOfficial, int64, error) {
	var items []model.GalgameOfficial
	var total int64

	r.db.WithContext(ctx).Model(&model.GalgameOfficial{}).Count(&total)

	err := r.db.WithContext(ctx).
		Preload("Alias").
		Order("(SELECT COUNT(*) FROM galgame_official_relation WHERE official_id = galgame_official.id) DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error

	return items, total, err
}

// FindByID finds an official by ID
func (r *OfficialRepository) FindByID(ctx context.Context, id int) (*model.GalgameOfficial, error) {
	var official model.GalgameOfficial
	err := r.db.WithContext(ctx).Preload("Alias").First(&official, id).Error
	return &official, err
}

// FindGalgamesByOfficialID returns galgames for an official
func (r *OfficialRepository) FindGalgamesByOfficialID(ctx context.Context, officialID, page, limit int, sortField, sortOrder string) ([]model.Galgame, int64, error) {
	var galgames []model.Galgame
	var total int64

	sub := r.db.WithContext(ctx).
		Model(&model.GalgameOfficialRelation{}).
		Select("galgame_id").
		Where("official_id = ?", officialID)

	query := r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id IN (?) AND status != 1", sub)

	query.Count(&total)

	if sortField == "" {
		sortField = "resource_update_time"
	}
	if sortOrder == "" {
		sortOrder = "desc"
	}

	err := query.
		Order(sortField + " " + sortOrder).
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&galgames).Error

	return galgames, total, err
}

// Search searches officials by name or alias
func (r *OfficialRepository) Search(ctx context.Context, terms []string) ([]model.GalgameOfficial, error) {
	var officials []model.GalgameOfficial

	query := r.db.WithContext(ctx).Model(&model.GalgameOfficial{})

	for _, term := range terms {
		like := "%" + strings.ToLower(term) + "%"
		aliasSubquery := r.db.Model(&model.GalgameOfficialAlias{}).
			Select("galgame_official_id").
			Where("LOWER(name) LIKE ?", like)
		query = query.Where(
			"LOWER(name) LIKE ? OR id IN (?)",
			like, aliasSubquery,
		)
	}

	err := query.Preload("Alias").Limit(50).Find(&officials).Error
	return officials, err
}

// Update updates an official and replaces aliases in a transaction
func (r *OfficialRepository) Update(ctx context.Context, officialID int, updates map[string]any, aliases []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&model.GalgameOfficial{}).Where("id = ?", officialID).Updates(updates).Error; err != nil {
				return err
			}
		}

		if aliases != nil {
			if err := tx.Where("galgame_official_id = ?", officialID).Delete(&model.GalgameOfficialAlias{}).Error; err != nil {
				return err
			}
			for _, name := range aliases {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if err := tx.Create(&model.GalgameOfficialAlias{
					GalgameOfficialID: officialID,
					Name:              name,
				}).Error; err != nil {
					return err
				}
			}
		}

		return nil
	})
}
