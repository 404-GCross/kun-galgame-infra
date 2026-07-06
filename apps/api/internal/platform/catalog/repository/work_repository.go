package repository

import (
	"context"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// WorkRepository reads the work registry.
type WorkRepository struct {
	db *gorm.DB
}

func NewWorkRepository(db *gorm.DB) *WorkRepository {
	return &WorkRepository{db: db}
}

func (r *WorkRepository) Get(ctx context.Context, id int64) (*model.CatalogWork, error) {
	var row model.CatalogWork
	err := r.db.WithContext(ctx).First(&row, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// FindClaimed returns the work already claimed by (medium, site,
// productWorkID), or nil — the idempotency lookup of ClaimWork.
func FindClaimed(tx *gorm.DB, mediumID int16, site string, productWorkID int64) (*model.CatalogWork, error) {
	var row model.CatalogWork
	err := tx.Where("medium_id = ? AND site = ? AND product_work_id = ?", mediumID, site, productWorkID).
		First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// FindWorkIDByExactRef returns the work exact-anchored to (sourceID,
// externalID), if any. The exact tier is unique per (source, external_id,
// entity_type), so at most one row can match.
func FindWorkIDByExactRef(tx *gorm.DB, sourceID int16, externalID string) (int64, bool, error) {
	var ref model.CatalogExternalRef
	err := tx.Where(
		"entity_type = ? AND source_id = ? AND external_id = ? AND link_kind = ?",
		model.EntityTypeWork, sourceID, externalID, model.LinkKindExact,
	).First(&ref).Error
	if err == gorm.ErrRecordNotFound {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return ref.EntityID, true, nil
}

// LockEntityRow locks the given entity's base-table row FOR UPDATE,
// serializing mutation transactions (and thereby revision numbering) per
// entity. Unknown entity types are a programming error.
func LockEntityRow(tx *gorm.DB, entityType int16, id int64) error {
	table, ok := entityTable(entityType)
	if !ok {
		return gorm.ErrInvalidData
	}
	return tx.Exec(`SELECT 1 FROM `+table+` WHERE id = ? FOR UPDATE`, id).Error
}

// entityTable maps an entity type constant to its base table.
func entityTable(entityType int16) (string, bool) {
	switch entityType {
	case model.EntityTypePerson:
		return "catalog_person", true
	case model.EntityTypeCreditName:
		return "catalog_credit_name", true
	case model.EntityTypeOrg:
		return "catalog_org", true
	case model.EntityTypeLabel:
		return "catalog_label", true
	case model.EntityTypeCharacter:
		return "catalog_character", true
	case model.EntityTypeWork:
		return "catalog_work", true
	case model.EntityTypeRelease:
		return "catalog_release", true
	}
	return "", false
}
