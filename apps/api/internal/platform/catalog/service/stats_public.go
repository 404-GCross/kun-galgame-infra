package service

import (
	"context"

	"api/internal/platform/catalog/model"
)

type PublicSummary struct {
	WorksTotal    int64
	WorksByMedium []PublicMediumCountRow
	Entities      PublicEntityCountRow
}

type PublicMediumCountRow struct {
	MediumID int16  `gorm:"column:medium_id"`
	Medium   string `gorm:"column:medium"`
	Count    int64  `gorm:"column:count"`
}

type PublicEntityCountRow struct {
	Labels      int64 `gorm:"column:labels"`
	Characters  int64 `gorm:"column:characters"`
	CreditNames int64 `gorm:"column:credit_names"`
	Persons     int64 `gorm:"column:persons"`
}

func (s *StatsService) PublicSummary(ctx context.Context) (*PublicSummary, error) {
	db := s.db.WithContext(ctx)
	out := &PublicSummary{}

	if err := db.Raw(`SELECT w.medium_id, m.key AS medium, count(*) AS count
		FROM catalog_work w JOIN catalog_medium m ON m.id = w.medium_id
		WHERE w.deleted_at IS NULL AND w.status = ?
		GROUP BY 1, 2 ORDER BY 1`, model.WorkStatusLive).Scan(&out.WorksByMedium).Error; err != nil {
		return nil, err
	}
	for _, r := range out.WorksByMedium {
		out.WorksTotal += r.Count
	}
	if err := db.Raw(`SELECT
		(SELECT count(*) FROM catalog_label WHERE deleted_at IS NULL) AS labels,
		(SELECT count(*) FROM catalog_character WHERE deleted_at IS NULL) AS characters,
		(SELECT count(*) FROM catalog_credit_name) AS credit_names,
		(SELECT count(*) FROM catalog_person WHERE deleted_at IS NULL) AS persons`).
		Scan(&out.Entities).Error; err != nil {
		return nil, err
	}
	return out, nil
}
