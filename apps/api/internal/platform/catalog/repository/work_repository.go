package repository

import (
	"context"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// LoadClaimedWorkIDs returns product_work_id → work_id for every work claimed
// by the given site — a read-only batch prefetch that lets a bulk
// reconciliation job map product rows onto their catalog identities without
// one round-trip per row.
func LoadClaimedWorkIDs(db *gorm.DB, site string) (map[int64]int64, error) {
	var rows []struct {
		ProductWorkID int64
		ID            int64
	}
	if err := db.Model(&model.CatalogWork{}).
		Select("product_work_id, id").
		Where("site = ? AND product_work_id IS NOT NULL", site).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.ProductWorkID] = r.ID
	}
	return out, nil
}

// InsertRefIfAbsent inserts a graded external ref, skipping when the same
// (entity_type, entity_id, source_id, external_id) already exists in ANY tier.
// Import pipelines NEVER re-grade an existing assertion (doc 17 R8 / step 11
// T0.5: promotion/demotion belongs to the human-review + merge paths); the
// four-column primary key drives the ON CONFLICT DO NOTHING. Returns whether a
// row was actually written.
func InsertRefIfAbsent(db *gorm.DB, ref model.CatalogExternalRef) (bool, error) {
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&ref)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
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
