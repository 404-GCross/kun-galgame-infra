package service

import (
	"context"

	"gorm.io/gorm"
)

type StatsService struct{ db *gorm.DB }

func NewStatsService(db *gorm.DB) *StatsService { return &StatsService{db: db} }

type Overview struct {
	WorksTotal   int64
	WorksCells   []WorksCellRow
	Entities     EntityCountRow
	Credits      []KeyCountRow
	Attributions []KindCountRow
	Anchors      []AnchorTierRow
	Candidates   []StatusCountRow
	Proposals    []StatusCountRow
	ProbableRefs int64
	Rejections   int64
	LLMBid       []KeyCountRow
	Freshness    []FreshnessRow
}

type WorksCellRow struct {
	MediumID int16 `gorm:"column:medium_id"`
	Claimed  bool  `gorm:"column:claimed"`
	Status   int16 `gorm:"column:status"`
	Count    int64 `gorm:"column:count"`
}
type EntityCountRow struct {
	Persons     int64 `gorm:"column:persons"`
	CreditNames int64 `gorm:"column:credit_names"`
	OrphanNames int64 `gorm:"column:orphan_names"`
	Labels      int64 `gorm:"column:labels"`
	Characters  int64 `gorm:"column:characters"`
}
type KeyCountRow struct {
	Key   string `gorm:"column:key"`
	Count int64  `gorm:"column:count"`
}
type KindCountRow struct {
	Kind  int16 `gorm:"column:kind"`
	Count int64 `gorm:"column:count"`
}
type AnchorTierRow struct {
	Source   string `gorm:"column:source"`
	LinkKind int16  `gorm:"column:link_kind"`
	Count    int64  `gorm:"column:count"`
}
type StatusCountRow struct {
	Status int16 `gorm:"column:status"`
	Count  int64 `gorm:"column:count"`
}
type FreshnessRow struct {
	Source    string `gorm:"column:source"`
	LatestRef string `gorm:"column:latest_ref"`
}

func (s *StatsService) Overview(ctx context.Context) (*Overview, error) {
	db := s.db.WithContext(ctx)
	o := &Overview{}

	if err := db.Raw(`SELECT count(*) FROM catalog_work`).Scan(&o.WorksTotal).Error; err != nil {
		return nil, err
	}
	if err := db.Raw(`SELECT medium_id, (site IS NOT NULL) AS claimed, status, count(*) AS count
		FROM catalog_work GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`).Scan(&o.WorksCells).Error; err != nil {
		return nil, err
	}
	if err := db.Raw(`SELECT
		(SELECT count(*) FROM catalog_person) AS persons,
		(SELECT count(*) FROM catalog_credit_name) AS credit_names,
		(SELECT count(*) FROM catalog_credit_name WHERE person_id IS NULL) AS orphan_names,
		(SELECT count(*) FROM catalog_label) AS labels,
		(SELECT count(*) FROM catalog_character) AS characters`).Scan(&o.Entities).Error; err != nil {
		return nil, err
	}
	if err := db.Raw(`SELECT coalesce(s.key, '') AS key, count(*) AS count
		FROM catalog_credit c LEFT JOIN catalog_source s ON s.id = c.source_id
		GROUP BY 1 ORDER BY 2 DESC`).Scan(&o.Credits).Error; err != nil {
		return nil, err
	}
	if err := db.Raw(`SELECT kind, count(*) AS count FROM catalog_work_label GROUP BY 1 ORDER BY 1`).Scan(&o.Attributions).Error; err != nil {
		return nil, err
	}
	if err := db.Raw(`SELECT s.key AS source, r.link_kind, count(*) AS count
		FROM catalog_external_ref r JOIN catalog_source s ON s.id = r.source_id
		GROUP BY 1, 2 ORDER BY 1, 2`).Scan(&o.Anchors).Error; err != nil {
		return nil, err
	}
	if err := db.Raw(`SELECT status, count(*) AS count FROM catalog_match_candidate GROUP BY 1 ORDER BY 1`).Scan(&o.Candidates).Error; err != nil {
		return nil, err
	}
	if err := db.Raw(`SELECT status, count(*) AS count FROM catalog_merge_proposal GROUP BY 1 ORDER BY 1`).Scan(&o.Proposals).Error; err != nil {
		return nil, err
	}
	if err := db.Raw(`SELECT count(*) FROM catalog_external_ref WHERE link_kind = 1`).Scan(&o.ProbableRefs).Error; err != nil {
		return nil, err
	}
	if err := db.Raw(`SELECT count(*) FROM catalog_match_rejection`).Scan(&o.Rejections).Error; err != nil {
		return nil, err
	}
	if s.tableExists("src_llm", "bid_identity_verdict") {
		if err := db.Raw(`SELECT CASE WHEN verdict = '' THEN 'deterministic' ELSE verdict END AS key, count(*) AS count
			FROM src_llm.bid_identity_verdict GROUP BY 1 ORDER BY 2 DESC`).Scan(&o.LLMBid).Error; err != nil {
			return nil, err
		}
	}
	if err := db.Raw(`SELECT s.key AS source, to_char(max(r.created_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS latest_ref
		FROM catalog_external_ref r JOIN catalog_source s ON s.id = r.source_id
		GROUP BY 1 ORDER BY 1`).Scan(&o.Freshness).Error; err != nil {
		return nil, err
	}
	return o, nil
}

func (s *StatsService) tableExists(schema, table string) bool {
	var n int64
	s.db.Raw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?`, schema, table).Scan(&n)
	return n > 0
}
