package repository

import (
	"context"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// RevisionRepository reads the polymorphic revision log.
type RevisionRepository struct {
	db *gorm.DB
}

func NewRevisionRepository(db *gorm.DB) *RevisionRepository {
	return &RevisionRepository{db: db}
}

// LatestByAction returns the newest revision of an entity with the given
// action (e.g. the merged_source snapshot an unmerge rebuilds from), or nil.
func (r *RevisionRepository) LatestByAction(ctx context.Context, entityType int16, entityID int64, action int16) (*model.CatalogRevision, error) {
	return LatestRevisionByAction(r.db.WithContext(ctx), entityType, entityID, action)
}

// LatestRevisionByAction is the tx-friendly variant.
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

// NextRevision returns the next per-entity revision number. Callers hold a
// lock on the entity row (every mutation transaction does), which serializes
// writers per entity; the UNIQUE(entity_type, entity_id, revision) index is
// the backstop that turns any residual race into a constraint error instead
// of a silent history fork.
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
