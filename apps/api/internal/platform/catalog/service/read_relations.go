package service

import (
	"context"
)

type WorkRelationRow struct {
	Key           string  `gorm:"column:key"`
	Phrase        string  `gorm:"column:phrase"`
	OtherID       int64   `gorm:"column:other_id"`
	DisplayName   string  `gorm:"column:display_name"`
	MediumID      int16   `gorm:"column:medium_id"`
	ContentRating int16   `gorm:"column:content_rating"`
	Status        int16   `gorm:"column:status"`
	Site          *string `gorm:"column:site"`
	ProductWorkID *int64  `gorm:"column:product_work_id"`
	ClaimState    *int16  `gorm:"column:claim_state"`
}

func (s *ReadService) loadWorkRelations(ctx context.Context, workID int64) ([]WorkRelationRow, error) {
	var rows []WorkRelationRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT rt.key, rt.forward_phrase AS phrase, w.id AS other_id, w.display_name,
		       w.medium_id, w.content_rating, w.status, w.site, w.product_work_id, w.claim_state
		FROM catalog_work_relation r
		JOIN catalog_relation_type rt ON rt.id = r.relation_type_id
		JOIN catalog_work w ON w.id = r.b_work_id AND w.deleted_at IS NULL
		WHERE r.a_work_id = ?
		UNION ALL
		SELECT rt.key,
		       CASE WHEN rt.is_symmetric THEN rt.forward_phrase ELSE rt.reverse_phrase END AS phrase,
		       w.id AS other_id, w.display_name,
		       w.medium_id, w.content_rating, w.status, w.site, w.product_work_id, w.claim_state
		FROM catalog_work_relation r
		JOIN catalog_relation_type rt ON rt.id = r.relation_type_id
		JOIN catalog_work w ON w.id = r.a_work_id AND w.deleted_at IS NULL
		WHERE r.b_work_id = ?
		ORDER BY key, other_id`, workID, workID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

const seriesRelationTypeID = 7

type SeriesSiblingRow struct {
	WorkID        int64   `gorm:"column:work_id"`
	DisplayName   string  `gorm:"column:display_name"`
	MediumID      int16   `gorm:"column:medium_id"`
	ContentRating int16   `gorm:"column:content_rating"`
	Status        int16   `gorm:"column:status"`
	Site          *string `gorm:"column:site"`
	ProductWorkID *int64  `gorm:"column:product_work_id"`
	ClaimState    *int16  `gorm:"column:claim_state"`
}

func (s *ReadService) loadSeriesSiblings(ctx context.Context, workID int64) ([]SeriesSiblingRow, error) {
	var nodes []int64
	if err := s.db.WithContext(ctx).Raw(`
		WITH RECURSIVE reach(node) AS (
			SELECT ?::bigint
			UNION
			SELECT x.other FROM reach rc CROSS JOIN LATERAL (
				SELECT r.b_work_id AS other FROM catalog_work_relation r
				WHERE r.relation_type_id = ? AND r.a_work_id = rc.node
				UNION ALL
				SELECT r.a_work_id FROM catalog_work_relation r
				WHERE r.relation_type_id = ? AND r.b_work_id = rc.node
			) x
		)
		SELECT node FROM reach WHERE node <> ?`,
		workID, seriesRelationTypeID, seriesRelationTypeID, workID).Scan(&nodes).Error; err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, nil
	}
	var rows []SeriesSiblingRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT w.id AS work_id, w.display_name, w.medium_id, w.content_rating, w.status, w.site, w.product_work_id, w.claim_state
		FROM catalog_work w
		WHERE w.id IN (?) AND w.deleted_at IS NULL
		ORDER BY w.id`, nodes).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
