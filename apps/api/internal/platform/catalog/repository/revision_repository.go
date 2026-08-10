package repository

import (
	"context"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type RevisionRepository struct {
	db *gorm.DB
}

func NewRevisionRepository(db *gorm.DB) *RevisionRepository {
	return &RevisionRepository{db: db}
}

func (r *RevisionRepository) LatestByAction(ctx context.Context, entityType int16, entityID int64, action int16) (*model.CatalogRevision, error) {
	return LatestRevisionByAction(r.db.WithContext(ctx), entityType, entityID, action)
}

func LatestRevisionByAction(db *gorm.DB, entityType int16, entityID int64, action int16) (*model.CatalogRevision, error) {
	var row model.CatalogRevision
	err := db.Where("entity_type = ? AND entity_id = ? AND action = ?", entityType, entityID, action).
		Order("revision DESC").First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func NextRevision(tx *gorm.DB, entityType int16, entityID int64) (int, error) {
	var maxRevision int
	err := tx.Model(&model.CatalogRevision{}).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		Select("COALESCE(MAX(revision), 0)").
		Scan(&maxRevision).Error
	if err != nil {
		return 0, err
	}
	return maxRevision + 1, nil
}
