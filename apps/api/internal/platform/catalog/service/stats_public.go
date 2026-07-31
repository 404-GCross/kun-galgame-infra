// stats_public.go — the SLIM public counts (GET /v1/catalog/stats, wave 149b).
//
// Its own queries, deliberately NOT a slice of Overview(): that dashboard runs
// a dozen aggregates over the review queues, the anchor tier matrix and the LLM
// verdict tables, none of which may reach the public face — and paying for them
// to then throw them away would make a cacheable product endpoint as expensive
// as the internal browser's whole page.
package service

import (
	"context"

	"api/internal/platform/catalog/model"
)

// PublicSummary is the slim public counts payload's raw rows; the handler maps
// it to dto.PublicCatalogStats.
type PublicSummary struct {
	WorksTotal    int64
	WorksByMedium []PublicMediumCountRow
	Entities      PublicEntityCountRow
}

// PublicMediumCountRow is one medium's LIVE work count, carrying the medium's
// public key alongside its id (resolved in SQL — the public face speaks string
// keys, never bare enum ints).
type PublicMediumCountRow struct {
	MediumID int16  `gorm:"column:medium_id"`
	Medium   string `gorm:"column:medium"`
	Count    int64  `gorm:"column:count"`
}

// PublicEntityCountRow totals the identity families that are publicly
// meaningful. Orgs are deliberately absent: they are an internal grouping layer
// behind labels, not a browsable public family.
type PublicEntityCountRow struct {
	Labels      int64 `gorm:"column:labels"`
	Characters  int64 `gorm:"column:characters"`
	CreditNames int64 `gorm:"column:credit_names"`
	Persons     int64 `gorm:"column:persons"`
}

// PublicSummary computes the public counts: LIVE works per medium (status=live,
// not soft-deleted — stubs and merged-away rows are not part of the catalogue)
// plus the live identity-family totals.
//
// R18 works ARE counted. These are aggregate numbers with no content attached:
// no title, no id, nothing renderable reaches the caller, so the r18 gate every
// item-serving lane carries has nothing to gate here. Splitting the counts by
// nsfw would instead publish exactly what the gate exists to hide (the size of
// the r18 population per caller).
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
	// catalog_credit_name has no soft-delete column (a merged name is rewritten
	// away, not tombstoned), so it is counted whole; the other three exclude
	// their soft-deleted rows.
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
