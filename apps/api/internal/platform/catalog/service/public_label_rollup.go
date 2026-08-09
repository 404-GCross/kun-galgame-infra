// public_label_rollup.go — the imprint ROLL-UP (wave 199).
//
// A holding company publishes nothing under its own name: the works hang off
// its imprints. Wave 186 gave us the arrows to know that (catalog_label_relation),
// but every read face still asked only "what is attributed to THIS label", so
// 311 company pages answered with an empty list while 10,668 works sat one hop
// below them — VISUAL ARTS showing 19 works with 553 under its 75 imprints,
// NEXTON showing 38 with 339 below.
//
// The roll-up is deliberately OPT-IN and deliberately ATTRIBUTED. Folding the
// children's works into work_count silently would erase the very arrow wave 186
// built the graph to hold: a reader could no longer tell that ダウンロード was
// published by Key, not by VisualArt's. So:
//
//   - work_count keeps meaning "attributed to this label", untouched;
//   - imprint_work_count is a SECOND number beside it, and the two never
//     overlap (a work the parent also claims is counted once, on the parent);
//   - the rolled-up page is reached by an explicit works?label_id=&label_rollup=1,
//     and every row that came up through a child carries via_label.
//
// That preserves the invariant the whole taxonomy lane is built on — the number
// beside a chip is the number you get by following it — for BOTH numbers:
// work_count == works?label_id=, and work_count + imprint_work_count ==
// works?label_id=&label_rollup=1.
package service

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

// labelRollupRelations are the DOWNWARD edges a roll-up follows: the label's
// imprints and its subsidiaries, one hop.
//
// The vocabulary's other downward-ish arrows are excluded on purpose. `spawned`
// names a spin-off — a company that LEFT, and whose catalogue is its own
// (fairys out of sprite); `succeeded_by` names a corporate succession, which
// wave 198's reviewers already ruled is two entities and one arrow, not one
// catalogue. Rolling either up would put another company's works on this page
// under this company's name, which is the failure the attribution exists to
// prevent.
var labelRollupRelations = []int16{model.LabelRelationSubsidiary, model.LabelRelationImprint}

// labelRollupChildren is a subquery yielding the one-hop roll-up children of
// ONE bound label id (two placeholders: the label id, then the relation list).
// Soft-deleted children are excluded at the join, exactly as the graph face
// rules: a merged-away label must not contribute works under its old identity.
const labelRollupChildren = `SELECT r.other_label_id FROM catalog_label_relation r
	JOIN catalog_label c ON c.id = r.other_label_id AND c.deleted_at IS NULL
	WHERE r.label_id = ? AND r.relation IN ?`

// labelImprintWorkEdge is the taxonomy count lane's edge shape (key_id,
// work_id) for the roll-up: a PARENT label keyed to the works its imprints and
// subsidiaries hold.
//
// The NOT EXISTS is what keeps the two numbers disjoint. Without it a work the
// parent also attributes to itself would be counted twice — once in work_count
// and once in imprint_work_count — and the sum would no longer be the length of
// the rolled-up list, which is the one promise this pair of numbers makes.
var labelImprintWorkEdge = fmt.Sprintf(`(SELECT r.label_id AS key_id, wl.work_id
	FROM catalog_label_relation r
	JOIN catalog_label c ON c.id = r.other_label_id AND c.deleted_at IS NULL
	JOIN catalog_work_label wl ON wl.label_id = r.other_label_id
	WHERE r.relation IN (%d, %d)
	  AND NOT EXISTS (SELECT 1 FROM catalog_work_label own
	    WHERE own.work_id = wl.work_id AND own.label_id = r.label_id)) e`,
	model.LabelRelationSubsidiary, model.LabelRelationImprint)

// labelRollupVia answers, for one page of works fetched under label_rollup=1,
// which child label carried each work in — the `via <imprint>` attribution.
//
// A work the SEED label attributes to itself is absent from the result: it is
// the company's own work and gets no via. A work held by several of the
// children reports the lowest child id, deterministically; the page is a list
// of works, not of attributions, and duplicating a row per imprint would break
// the count equality this file exists to keep.
func (s *PublicService) labelRollupVia(ctx context.Context, seedID int64, workIDs []int64) (map[int64]dto.PublicLabelVia, error) {
	out := map[int64]dto.PublicLabelVia{}
	if seedID <= 0 || len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		WorkID  int64  `gorm:"column:work_id"`
		LabelID int64  `gorm:"column:label_id"`
		Name    string `gorm:"column:display_name"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (wl.work_id) wl.work_id, wl.label_id, c.display_name
		FROM catalog_work_label wl
		JOIN catalog_label c ON c.id = wl.label_id AND c.deleted_at IS NULL
		WHERE wl.work_id IN ?
		  AND wl.label_id IN (`+labelRollupChildren+`)
		  AND NOT EXISTS (SELECT 1 FROM catalog_work_label own
		    WHERE own.work_id = wl.work_id AND own.label_id = ?)
		ORDER BY wl.work_id, wl.label_id`,
		workIDs, seedID, labelRollupRelations, seedID,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.WorkID] = dto.PublicLabelVia{ID: r.LabelID, Name: r.Name}
	}
	return out, nil
}

// imprintWorkCount is the roll-up companion to the browse lane's work_count:
// how many MORE works the caller reaches by following label_rollup=1. Same
// nsfw-aware, live-claim aggregate — it is the same function over a different
// edge, so the two numbers cannot drift into meaning different populations.
func (s *PublicService) imprintWorkCount(ctx context.Context, id int64, nsfw bool) (int, error) {
	counts, err := s.workCountsFor(ctx, labelImprintWorkEdge, []int64{id}, nsfw)
	if err != nil {
		return 0, err
	}
	return counts[id], nil
}
