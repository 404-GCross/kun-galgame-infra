package doujinbangumi

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

const minTitleLen = 4

type registry struct {
	galgameMedium int16
	bangumiSource int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&r.galgameMedium).Error; err != nil {
		return r, fmt.Errorf("resolve galgame medium: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&r.bangumiSource).Error; err != nil {
		return r, fmt.Errorf("resolve bangumi source: %w", err)
	}
	if r.galgameMedium == 0 || r.bangumiSource == 0 {
		return r, fmt.Errorf("registry not seeded (galgame medium=%d, bangumi source=%d)", r.galgameMedium, r.bangumiSource)
	}
	return r, nil
}

type candTitle struct {
	WorkID      int64  `gorm:"column:work_id"`
	DisplayName string `gorm:"column:display_name"`
	TitleNorm   string `gorm:"column:title_norm"`
}

func loadCandidateTitles(ctx context.Context, db *gorm.DB, reg registry) ([]candTitle, error) {
	var out []candTitle
	err := db.WithContext(ctx).Raw(
		`SELECT w.id AS work_id, w.display_name AS display_name, t.title_norm AS title_norm
			FROM catalog_work w
			JOIN catalog_work_title t ON t.work_id = w.id
			WHERE w.medium_id = ? AND (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL
				AND length(t.title_norm) >= ?
				AND `+editspec.NotSuppressedWorkTitleSQL("t")+`
			ORDER BY w.id`,
		reg.galgameMedium, minTitleLen,
	).Scan(&out).Error
	return out, err
}

type subjectRow struct {
	ID         int64  `gorm:"column:id"`
	Name       string `gorm:"column:name"`
	NameNorm   string `gorm:"column:name_norm"`
	NameCNNorm string `gorm:"column:name_cn_norm"`
	Year       int    `gorm:"column:year"`
}

func loadType4Subjects(ctx context.Context, db *gorm.DB) ([]subjectRow, error) {
	var out []subjectRow
	err := db.WithContext(ctx).Raw(
		`SELECT id, name, name_norm, name_cn_norm,
				CASE WHEN substring(date from 1 for 4) ~ '^[0-9]{4}$'
					  AND substring(date from 1 for 4)::int BETWEEN 1900 AND 2100
					 THEN substring(date from 1 for 4)::int ELSE 0 END AS year
			FROM src_bangumi.subject WHERE type = 4`,
	).Scan(&out).Error
	return out, err
}

func loadWorkYears(ctx context.Context, db *gorm.DB, reg registry) (map[int64]int, error) {
	var rows []struct {
		WorkID int64 `gorm:"column:work_id"`
		Year   int   `gorm:"column:year"`
	}
	if err := db.WithContext(ctx).Raw(
		`SELECT rel.work_id AS work_id, min(rel.released_y) AS year
			FROM catalog_release rel
			JOIN catalog_work w ON w.id = rel.work_id
			WHERE w.medium_id = ? AND (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL
				AND rel.released_y IS NOT NULL AND rel.deleted_at IS NULL
			GROUP BY rel.work_id`,
		reg.galgameMedium,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int, len(rows))
	for _, r := range rows {
		out[r.WorkID] = r.Year
	}
	return out, nil
}

func loadAnchoredWorks(ctx context.Context, db *gorm.DB, reg registry) (map[int64]struct{}, error) {
	var ids []int64
	if err := db.WithContext(ctx).Raw(
		`SELECT DISTINCT e.entity_id
			FROM catalog_external_ref e
			JOIN catalog_work w ON w.id = e.entity_id
			WHERE e.entity_type = ? AND e.source_id = ?
				AND w.medium_id = ? AND (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL`,
		model.EntityTypeWork, reg.bangumiSource, reg.galgameMedium,
	).Scan(&ids).Error; err != nil {
		return nil, err
	}
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set, nil
}
