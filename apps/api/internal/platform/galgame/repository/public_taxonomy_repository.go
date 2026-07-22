package repository

import (
	"context"

	"api/internal/platform/galgame/model"
)

// Data-access additions for the NextMoe open-API public taxonomy projection
// (W1b). The tag / official / series read paths reuse the existing repository
// methods (List / FindByID / GalgameIDsBy* / FindGalgamesByMultipleTags) as-is;
// only the engine list needs a NEW paginated variant, because the bridge
// EngineRepository.ListAll returns the whole set as a bare array and the /v1
// face uniformly serves {items, total} page/limit envelopes.

// PublicEngineListPaged returns a page of engines ordered by published galgame
// count (descending), plus the total engine count. It mirrors ListAll's cnt
// LEFT JOIN (count only status=0 galgames so the number matches the detail page)
// but adds OFFSET/LIMIT keyset-free pagination for the public list envelope.
func (r *EngineRepository) PublicEngineListPaged(ctx context.Context, page, limit int) ([]model.GalgameEngine, int64, error) {
	var items []model.GalgameEngine
	var total int64

	if err := r.db.WithContext(ctx).Model(&model.GalgameEngine{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := r.db.WithContext(ctx).
		Select("galgame_engine.*, COALESCE(ec.cnt, 0) AS cnt").
		Joins("LEFT JOIN (SELECT r.engine_id, COUNT(*) AS cnt FROM galgame_engine_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.engine_id) ec ON ec.engine_id = galgame_engine.id").
		Order("cnt DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error

	return items, total, err
}
