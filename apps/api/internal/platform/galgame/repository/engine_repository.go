package repository

import (
	"context"
	"encoding/json"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// EngineRepository handles engine data access
type EngineRepository struct {
	db *gorm.DB
}

// NewEngineRepository creates a new EngineRepository
func NewEngineRepository(db *gorm.DB) *EngineRepository {
	return &EngineRepository{db: db}
}

// ListAll returns all engines with galgame counts (no pagination, small dataset)
func (r *EngineRepository) ListAll(ctx context.Context) ([]model.GalgameEngine, error) {
	var items []model.GalgameEngine
	err := r.db.WithContext(ctx).
		Select("galgame_engine.*, COALESCE(ec.cnt, 0) AS cnt").
		Joins("LEFT JOIN (SELECT engine_id, COUNT(*) AS cnt FROM galgame_engine_relation GROUP BY engine_id) ec ON ec.engine_id = galgame_engine.id").
		Order("cnt DESC").
		Find(&items).Error
	return items, err
}

// FindByID finds an engine by ID
func (r *EngineRepository) FindByID(ctx context.Context, id int) (*model.GalgameEngine, error) {
	var engine model.GalgameEngine
	err := r.db.WithContext(ctx).First(&engine, id).Error
	return &engine, err
}

// FindGalgamesByEngineID returns galgames for an engine
func (r *EngineRepository) FindGalgamesByEngineID(ctx context.Context, engineID, page, limit int) ([]model.Galgame, int64, error) {
	var galgames []model.Galgame
	var total int64

	sub := r.db.WithContext(ctx).
		Model(&model.GalgameEngineRelation{}).
		Select("galgame_id").
		Where("engine_id = ?", engineID)

	query := r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id IN (?) AND status = 0", sub)

	query.Count(&total)

	err := query.
		Order("resource_update_time DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&galgames).Error

	return galgames, total, err
}

// Update updates an engine
func (r *EngineRepository) Update(ctx context.Context, engineID int, updates map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.GalgameEngine{}).Where("id = ?", engineID).Updates(updates).Error
}

// MarshalAlias converts a string slice to JSON for the alias field
func MarshalAlias(aliases []string) []byte {
	data, _ := json.Marshal(aliases)
	return data
}
