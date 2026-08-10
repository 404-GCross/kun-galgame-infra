package repository

import (
	"context"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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

func FindWorkIDByAnchor(tx *gorm.DB, sourceID int16, externalID string) (int64, bool, error) {
	var ref struct {
		EntityType int16
		EntityID   int64
	}
	if err := tx.Raw(`SELECT entity_type, entity_id FROM catalog_external_ref
		WHERE source_id = ? AND external_id = ? AND link_kind = ? AND entity_type IN (?, ?)
		ORDER BY entity_type ASC LIMIT 1`,
		sourceID, externalID, model.LinkKindExact, model.EntityTypeWork, model.EntityTypeRelease).Scan(&ref).Error; err != nil {
		return 0, false, err
	}
	if ref.EntityID == 0 {
		return 0, false, nil
	}
	if ref.EntityType == model.EntityTypeRelease {
		var workID int64
		if err := tx.Raw(`SELECT work_id FROM catalog_release WHERE id = ?`, ref.EntityID).Scan(&workID).Error; err != nil {
			return 0, false, err
		}
		return workID, workID != 0, nil
	}
	return ref.EntityID, true, nil
}

func LockEntityRow(tx *gorm.DB, entityType int16, id int64) error {
	table, ok := entityTable(entityType)
	if !ok {
		return gorm.ErrInvalidData
	}
	return tx.Exec(`SELECT 1 FROM `+table+` WHERE id = ? FOR UPDATE`, id).Error
}

func LoadClaimedWorkIDs(db *gorm.DB, site string, productWorkIDs ...int64) (map[int64]int64, error) {
	var rows []struct {
		ProductWorkID int64
		ID            int64
	}
	q := db.Model(&model.CatalogWork{}).
		Select("product_work_id, id").
		Where("site = ? AND product_work_id IS NOT NULL", site)
	if len(productWorkIDs) > 0 {
		q = q.Where("product_work_id IN ?", productWorkIDs)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.ProductWorkID] = r.ID
	}
	return out, nil
}

func InsertRefIfAbsent(db *gorm.DB, ref model.CatalogExternalRef) (bool, error) {
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&ref)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func entityTable(entityType int16) (string, bool) {
	switch entityType {
	case model.EntityTypePerson:
		return "catalog_person", true
	case model.EntityTypeCreditName:
		return "catalog_credit_name", true
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
