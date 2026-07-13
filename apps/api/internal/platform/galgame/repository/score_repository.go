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

// LoadAllStats returns every galgame_stats snapshot row (the six frozen v1
// keys). Order is unspecified; the caller keys by Key. Empty when the build job
// has never run.
func (r *GalgameRepository) LoadAllStats(ctx context.Context) ([]model.GalgameStats, error) {
	var rows []model.GalgameStats
	err := r.db.WithContext(ctx).Find(&rows).Error
	return rows, err
}
