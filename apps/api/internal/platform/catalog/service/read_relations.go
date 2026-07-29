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

// seriesRelationTypeID is catalog_relation_type.id for same_series (seed key
// "same_series", symmetric). vndb's ser edges project here (importer type 7).
const seriesRelationTypeID = 7

// SeriesSiblingRow is one work in the viewed work's same_series transitive
// closure (wave 113). Identity rides along so the consumer renders a brief
// without a second query.
type SeriesSiblingRow struct {
	WorkID        int64   `gorm:"column:work_id"`
	DisplayName   string  `gorm:"column:display_name"`
	MediumID      int16   `gorm:"column:medium_id"`
	ContentRating int16   `gorm:"column:content_rating"`
	Status        int16   `gorm:"column:status"`
	Site          *string `gorm:"column:site"`
	ProductWorkID *int64  `gorm:"column:product_work_id"`
}

// loadSeriesSiblings computes the transitive closure of the same_series (type 7)
// relation from workID via a recursive CTE (a connected-component walk — UNION
// dedups and terminates), returning every OTHER live work in the same series
// component. same_series is symmetric and, as an equivalence relation,
// transitive: a leaf work (wave 113 measured 68.6% of series nodes are degree-1
// leaves) sees its WHOLE family here, not just the one neighbour the pairwise
// relations face records. Self is excluded; deleted ends drop.
//
// Wave 117 splits this into two statements — the walk and the briefs — because
// this runs on EVERY work detail read (S2S works/{id}, by-anchor, public
// /v1/catalog/works/{id}) and the old single-statement form was unconditionally
// quadratic-ish: the materialised `edges` UNION ALL view cannot be
// parameterised, so the planner re-scanned the whole relation table twice per
// recursion round (prod: 87.5ms / 6,699 buffers, plus a 226k-row hash over
// catalog_work). The LATERAL shape below probes per node instead, so the two
// covering partial indexes (idx_catalog_work_relation_series_a/_b, see
// migrate.go) serve the whole walk as Index Only Scans. Keep the LATERAL form:
// measurements showed an equivalent UNION-ALL-view form never gets the probe.
func (s *ReadService) loadSeriesSiblings(ctx context.Context, workID int64) ([]SeriesSiblingRow, error) {
	// Stage 1 — the connected component's node set. UNION (not UNION ALL)
	// dedups, which is what terminates the walk on cycles.
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
	// Stage 2 — briefs. The 98.2% of works with no series edge stop here and
	// never touch catalog_work at all; nil matches what the old form scanned
	// into for an empty walk (the handler/public projections pre-size non-nil).
	if len(nodes) == 0 {
		return nil, nil
	}
	var rows []SeriesSiblingRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT w.id AS work_id, w.display_name, w.medium_id, w.content_rating, w.status, w.site, w.product_work_id
		FROM catalog_work w
		WHERE w.id IN (?) AND w.deleted_at IS NULL
		ORDER BY w.id`, nodes).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
