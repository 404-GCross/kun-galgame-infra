package repository

import (
	"context"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// ScoreMeta bundles a galgame's three cross-source narrow-table rows plus the
// galgame's own vndb_id (the anchor for the VNDB attribution URL — step 34 T2
// pins vndb_id to the galgame row). Each meta pointer is nil when no row anchors
// that source; VNDBID is "" when the galgame row is absent.
type ScoreMeta struct {
	VNDBID  string
	VNDB    *model.GalgameVNDBMeta
	Bangumi *model.GalgameBangumiMeta
	EG      *model.GalgameEGMeta
}

// LoadScoreMeta reads the vndb_id anchor + the three narrow score rows for one
// galgame. A missing narrow row (or a missing galgame) is not an error — the
// caller renders it as a null source object.
func (r *GalgameRepository) LoadScoreMeta(ctx context.Context, gid int) (ScoreMeta, error) {
	var out ScoreMeta

	// vndb_id anchor lives on the galgame row (mirrors galgame_vndb_meta.vndb_id).
	if err := r.db.WithContext(ctx).Model(&model.Galgame{}).
		Select("vndb_id").Where("id = ?", gid).
		Limit(1).Scan(&out.VNDBID).Error; err != nil {
		return out, err
	}

	load := func(dst any) error {
		err := r.db.WithContext(ctx).Where("galgame_id = ?", gid).First(dst).Error
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}

	var vndb model.GalgameVNDBMeta
	if err := load(&vndb); err != nil {
		return out, err
	} else if vndb.GalgameID != 0 {
		out.VNDB = &vndb
	}

	var bgm model.GalgameBangumiMeta
	if err := load(&bgm); err != nil {
		return out, err
	} else if bgm.GalgameID != 0 {
		out.Bangumi = &bgm
	}

	var eg model.GalgameEGMeta
	if err := load(&eg); err != nil {
		return out, err
	} else if eg.GalgameID != 0 {
		out.EG = &eg
	}

	return out, nil
}

// LoadScoreMetaBatch reads the three narrow score rows for a whole page of ids
// in exactly three IN queries (one per meta table) — the batch loader behind the
// open-API list/search include=scores expansion (step 07 裁定 2: never one query
// per item). The returned map holds an entry for every id that anchors at least
// one source; an absent id (its zero-value ScoreMeta) projects to an all-null
// scores block, same as the per-id path. VNDBID is sourced from the vndb meta
// row (which carries vndb_id) — it is consumed only when that row is present, so
// no separate galgame lookup is needed.
func (r *GalgameRepository) LoadScoreMetaBatch(ctx context.Context, ids []int) (map[int]ScoreMeta, error) {
	out := make(map[int]ScoreMeta, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	var vndb []model.GalgameVNDBMeta
	if err := r.db.WithContext(ctx).Where("galgame_id IN ?", ids).Find(&vndb).Error; err != nil {
		return nil, err
	}
	for i := range vndb {
		m := vndb[i]
		sm := out[m.GalgameID]
		sm.VNDB = &m
		sm.VNDBID = m.VNDBID
		out[m.GalgameID] = sm
	}

	var bgm []model.GalgameBangumiMeta
	if err := r.db.WithContext(ctx).Where("galgame_id IN ?", ids).Find(&bgm).Error; err != nil {
		return nil, err
	}
	for i := range bgm {
		m := bgm[i]
		sm := out[m.GalgameID]
		sm.Bangumi = &m
		out[m.GalgameID] = sm
	}

	var eg []model.GalgameEGMeta
	if err := r.db.WithContext(ctx).Where("galgame_id IN ?", ids).Find(&eg).Error; err != nil {
		return nil, err
	}
	for i := range eg {
		m := eg[i]
		sm := out[m.GalgameID]
		sm.EG = &m
		out[m.GalgameID] = sm
	}

	return out, nil
}

// LoadAllStats returns every galgame_stats snapshot row (the six frozen v1
// keys). Order is unspecified; the caller keys by Key. Empty when the build job
// has never run.
func (r *GalgameRepository) LoadAllStats(ctx context.Context) ([]model.GalgameStats, error) {
	var rows []model.GalgameStats
	err := r.db.WithContext(ctx).Find(&rows).Error
	return rows, err
}
