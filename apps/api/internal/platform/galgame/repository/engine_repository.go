package repository

import (
	"context"
	"encoding/json"

	"api/internal/platform/galgame/model"

	"gorm.io/datatypes"
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

// DB exposes the underlying *gorm.DB so the taxonomy service can open
// transactions that span this repo and others (matches the pattern on
// TagRepository / SeriesRepository).
func (r *EngineRepository) DB() *gorm.DB { return r.db }

// ListAll returns all engines with galgame counts (no pagination, small dataset)
func (r *EngineRepository) ListAll(ctx context.Context) ([]model.GalgameEngine, error) {
	var items []model.GalgameEngine
	err := r.db.WithContext(ctx).
		Select("galgame_engine.*, COALESCE(ec.cnt, 0) AS cnt").
		// Count only published galgames so the list total matches the detail
		// page (FindGalgamesByEngineID filters status=0).
		Joins("LEFT JOIN (SELECT r.engine_id, COUNT(*) AS cnt FROM galgame_engine_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.engine_id) ec ON ec.engine_id = galgame_engine.id").
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

// FindGalgamesByEngineID returns galgames for an engine.
// If contentLimit is non-empty ("sfw" or "nsfw"), filters galgames accordingly
// so total / pagination reflects only matching entries.
func (r *EngineRepository) FindGalgamesByEngineID(ctx context.Context, engineID, page, limit int, contentLimit string) ([]model.Galgame, int64, error) {
	var galgames []model.Galgame
	var total int64

	sub := r.db.WithContext(ctx).
		Model(&model.GalgameEngineRelation{}).
		Select("galgame_id").
		Where("engine_id = ?", engineID)

	query := r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id IN (?) AND status = 0", sub)

	if contentLimit != "" {
		query = query.Where("content_limit = ?", contentLimit)
	}

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

// ExistsByName reports whether an engine with this exact name exists.
func (r *EngineRepository) ExistsByName(ctx context.Context, name string) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.GalgameEngine{}).
		Where("name = ?", name).Count(&n).Error
	return n > 0, err
}

// Create inserts a new engine. Aliases live inline as a jsonb array on
// the row (engine has no separate alias table, unlike tag/official).
func (r *EngineRepository) Create(ctx context.Context, engine *model.GalgameEngine, aliases []string) error {
	if aliases == nil {
		aliases = []string{}
	}
	engine.Alias = datatypes.JSON(MarshalAlias(aliases))
	return r.db.WithContext(ctx).Create(engine).Error
}

// CountReferences returns how many galgame relations point at this
// engine. Engine has no separate alias table (alias is inline jsonb on
// the row), so only relations are counted.
func (r *EngineRepository) CountReferences(ctx context.Context, id int) (relations int64, err error) {
	err = r.db.WithContext(ctx).Model(&model.GalgameEngineRelation{}).
		Where("engine_id = ?", id).Count(&relations).Error
	return
}

// Delete removes an engine with its galgame relations.
func (r *EngineRepository) Delete(ctx context.Context, id int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("engine_id = ?", id).
			Delete(&model.GalgameEngineRelation{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.GalgameEngine{}, id).Error
	})
}

// MarshalAlias converts a string slice to JSON for the alias field
func MarshalAlias(aliases []string) []byte {
	data, _ := json.Marshal(aliases)
	return data
}
