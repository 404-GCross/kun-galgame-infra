package service

import (
	"context"
)

// WorkRelationRow is one cross-media relation edge rendered single-directionally
// from the viewed work's perspective (wave 104 — the S2S read face finally
// surfaces the REL1 edge table). Phrase is the direction-resolved display
// phrase (forward for outgoing and symmetric edges, reverse otherwise). The
// other end's identity fields ride along so the consumer renders a brief
// without a second query; the internal face carries r18 ends verbatim (the
// PUBLIC face drops them per its own nsfw gate).
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
}

// loadWorkRelations reads a work's relation edges (both directions, one query)
// joined to the other end's live work row. Deleted ends drop; everything else
// is the caller's concern (per-face policy).
func (s *ReadService) loadWorkRelations(ctx context.Context, workID int64) ([]WorkRelationRow, error) {
	var rows []WorkRelationRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT rt.key, rt.forward_phrase AS phrase, w.id AS other_id, w.display_name,
		       w.medium_id, w.content_rating, w.status, w.site, w.product_work_id
		FROM catalog_work_relation r
		JOIN catalog_relation_type rt ON rt.id = r.relation_type_id
		JOIN catalog_work w ON w.id = r.b_work_id AND w.deleted_at IS NULL
		WHERE r.a_work_id = ?
		UNION ALL
		SELECT rt.key,
		       CASE WHEN rt.is_symmetric THEN rt.forward_phrase ELSE rt.reverse_phrase END AS phrase,
		       w.id AS other_id, w.display_name,
		       w.medium_id, w.content_rating, w.status, w.site, w.product_work_id
		FROM catalog_work_relation r
		JOIN catalog_relation_type rt ON rt.id = r.relation_type_id
		JOIN catalog_work w ON w.id = r.a_work_id AND w.deleted_at IS NULL
		WHERE r.b_work_id = ?
		ORDER BY key, other_id`, workID, workID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
