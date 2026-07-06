// Package repository is the data-access layer of the catalog platform
// domain. Read paths hold their own *gorm.DB; multi-table write paths are
// package-level helpers taking the caller's transaction (the galgame-domain
// convention).
package repository

import (
	"context"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// RedirectRepository reads the polymorphic redirect table.
type RedirectRepository struct {
	db *gorm.DB
}

func NewRedirectRepository(db *gorm.DB) *RedirectRepository {
	return &RedirectRepository{db: db}
}

// Get returns the redirect for (entityType, oldID), or nil when the id is
// canonical (no redirect).
func (r *RedirectRepository) Get(ctx context.Context, entityType int16, oldID int64) (*model.CatalogRedirect, error) {
	return getRedirect(r.db.WithContext(ctx), entityType, oldID)
}

// GetBatch returns redirects for the given ids keyed by old_id (missing ids
// are canonical).
func (r *RedirectRepository) GetBatch(ctx context.Context, entityType int16, ids []int64) (map[int64]int64, error) {
	var rows []model.CatalogRedirect
	if err := r.db.WithContext(ctx).
		Where("entity_type = ? AND old_id IN ?", entityType, ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, row := range rows {
		out[row.OldID] = row.CurrentID
	}
	return out, nil
}

// RedirectCursor is the keyset cursor of the redirect feed: strictly
// increasing (merged_at, entity_type, old_id). The zero value starts from
// the beginning.
type RedirectCursor struct {
	MergedAt   time.Time
	EntityType int16
	OldID      int64
}

// Since returns up to limit redirects after the cursor, oldest first —
// the data source of the per-product-site cleanup cron (doc 10 §5.3).
// entityType filters to one entity family when non-nil.
func (r *RedirectRepository) Since(ctx context.Context, entityType *int16, cursor RedirectCursor, limit int) ([]model.CatalogRedirect, error) {
	q := r.db.WithContext(ctx).
		Where("(merged_at, entity_type, old_id) > (?, ?, ?)", cursor.MergedAt, cursor.EntityType, cursor.OldID).
		Order("merged_at, entity_type, old_id").
		Limit(limit)
	if entityType != nil {
		q = q.Where("entity_type = ?", *entityType)
	}
	var rows []model.CatalogRedirect
	err := q.Find(&rows).Error
	return rows, err
}

// getRedirect is the tx-friendly single lookup.
func getRedirect(db *gorm.DB, entityType int16, oldID int64) (*model.CatalogRedirect, error) {
	var row model.CatalogRedirect
	err := db.Where("entity_type = ? AND old_id = ?", entityType, oldID).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ResolveTx resolves an id inside a transaction (one lookup, no chain
// following — flattening keeps chains length one).
func ResolveTx(tx *gorm.DB, entityType int16, id int64) (int64, error) {
	row, err := getRedirect(tx, entityType, id)
	if err != nil {
		return 0, err
	}
	if row == nil {
		return id, nil
	}
	return row.CurrentID, nil
}

// FlattenRedirectsTo repoints every redirect currently targeting source at
// target — the write-time flattening that keeps every chain length one
// (doc 10 invariant 3).
func FlattenRedirectsTo(tx *gorm.DB, entityType int16, sourceID, targetID int64) error {
	return tx.Model(&model.CatalogRedirect{}).
		Where("entity_type = ? AND current_id = ?", entityType, sourceID).
		Update("current_id", targetID).Error
}

// InsertRedirect writes the source→target redirect row of a merge.
func InsertRedirect(tx *gorm.DB, entityType int16, oldID, currentID int64, mergedBy *int64, reason string) error {
	now := time.Now()
	return tx.Create(&model.CatalogRedirect{
		EntityType: entityType,
		OldID:      oldID,
		CurrentID:  currentID,
		MergedAt:   &now,
		MergedBy:   mergedBy,
		Reason:     reason,
	}).Error
}

// RepointRedirect updates one redirect row's target (unmerge reversal).
func RepointRedirect(tx *gorm.DB, entityType int16, oldID, newCurrentID int64) error {
	return tx.Model(&model.CatalogRedirect{}).
		Where("entity_type = ? AND old_id = ?", entityType, oldID).
		Update("current_id", newCurrentID).Error
}
