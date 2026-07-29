// public_taxonomy.go — the taxonomy read faces (A2-1b, refs/proj/126 D3): the
// labels / tags / engines browse lanes plus the engine detail record. Pure
// add-only DB read surface; the keyset / limit / cursor posture is the works
// list's verbatim so every /v1/catalog browse lane pages identically.
//
// work_count is the wave's load-bearing invariant. It is NSFW-AWARE: the number
// a caller gets is the number of works that same caller would actually page
// through via works?label_id=/tag_id=/engine_id=. The count query therefore
// reuses the works-list population predicate literally (LIVE galgame, not
// soft-deleted, r18 dropped unless nsfw) instead of counting edge rows. The
// deprecated galgame face shipped the opposite — official.galgame_count sat at
// a permanent 0 beside a non-empty member list — and that is the failure this
// face is written to avoid.
//
// Cost: ONE batched GROUP BY per page, never one count per row.
//
// Head-row semantics per family (grounded 2026-07-29):
//   - labels ARE merge-capable (catalog_label is in the merge entity table and
//     a merge soft-deletes the losing row + writes a catalog_redirect), so the
//     lane filters deleted_at IS NULL — merged-away labels leave the list and
//     stay resolvable through /v1/catalog/redirects.
//   - tags and engines are NOT merge-capable (neither table is in the merge
//     entity table, and neither model carries a soft-delete column), so every
//     row is a head row and there is nothing to filter.
package service

import (
	"context"
	"encoding/json"
	"strings"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
)

// Cursor lanes of the three taxonomy browse lanes. Each is its own lane token
// so a cursor can never be replayed across families (decodePublicCursor pins
// it).
const (
	taxonomyLaneLabels  = "labels"
	taxonomyLaneTags    = "tags"
	taxonomyLaneEngines = "engines"
)

// LabelsListFilter is the label browse lane's request shape.
type LabelsListFilter struct {
	Kind *int16 // model.LabelKind* — nil = every kind
	NSFW bool
}

// TagsListFilter is the canonical-tag browse lane's request shape.
type TagsListFilter struct {
	Tier *int16 // model.TagTier* — nil = every tier
	Kind *int16 // model.TagKind* — nil = every kind
	NSFW bool
}

// EnginesListFilter is the engine browse lane's request shape. The facet is a
// few hundred hand-curated rows, so it has no filters yet — the struct exists
// so nsfw travels the same way it does on the other two lanes.
type EnginesListFilter struct {
	NSFW bool
}

// LabelsList serves the keyset label browse lane (id ASC). A malformed cursor
// is ErrBadCursor (the handler maps it to 400).
func (s *PublicService) LabelsList(ctx context.Context, f LabelsListFilter, cursor string, limit int) (dto.PublicLabelsListData, error) {
	cur, err := decodePublicCursor(cursor, taxonomyLaneLabels)
	if err != nil {
		return dto.PublicLabelsListData{}, err
	}
	limit = clampBrowseLimit(limit)

	// Raw SQL bypasses GORM soft-delete, so deleted_at is filtered explicitly
	// (see the head-row note in the file doc).
	where := []string{"deleted_at IS NULL"}
	var args []any
	if f.Kind != nil {
		where = append(where, "kind = ?")
		args = append(args, *f.Kind)
	}
	// Snapshot the FILTER-only predicate before the cursor clause joins it: the
	// total counts the whole filtered set, not the tail after the cursor.
	filterWhere, filterArgs := append([]string(nil), where...), append([]any(nil), args...)
	if cur.ID > 0 {
		where = append(where, "id > ?")
		args = append(args, cur.ID)
	}
	args = append(args, limit)

	var rows []struct {
		ID          int64
		DisplayName string
		Kind        int16
	}
	q := `SELECT id, display_name, kind FROM catalog_label WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY id ASC LIMIT ?`
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return dto.PublicLabelsListData{}, err
	}

	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	counts, err := s.workCountsFor(ctx, labelWorkEdge, ids, f.NSFW)
	if err != nil {
		return dto.PublicLabelsListData{}, err
	}
	out := dto.PublicLabelsListData{Items: make([]dto.PublicLabelListItem, len(rows))}
	for i, r := range rows {
		out.Items[i] = dto.PublicLabelListItem{
			ID: r.ID, DisplayName: r.DisplayName, Kind: labelKindKey(r.Kind), WorkCount: counts[r.ID],
		}
	}
	if out.Total, err = s.taxonomyTotal(ctx, "catalog_label", filterWhere, filterArgs); err != nil {
		return dto.PublicLabelsListData{}, err
	}
	out.NextCursor = taxonomyNextCursor(taxonomyLaneLabels, ids, limit)
	return out, nil
}

// TagsList serves the keyset canonical-tag browse lane (id ASC).
func (s *PublicService) TagsList(ctx context.Context, f TagsListFilter, cursor string, limit int) (dto.PublicTagsListData, error) {
	cur, err := decodePublicCursor(cursor, taxonomyLaneTags)
	if err != nil {
		return dto.PublicTagsListData{}, err
	}
	limit = clampBrowseLimit(limit)

	var where []string
	var args []any
	if f.Tier != nil {
		where = append(where, "tier = ?")
		args = append(args, *f.Tier)
	}
	if f.Kind != nil {
		where = append(where, "kind = ?")
		args = append(args, *f.Kind)
	}
	// Filter-only predicate for the total (see LabelsList).
	filterWhere, filterArgs := append([]string(nil), where...), append([]any(nil), args...)
	if cur.ID > 0 {
		where = append(where, "id > ?")
		args = append(args, cur.ID)
	}
	args = append(args, limit)

	var rows []struct {
		ID   int64
		Name string
		Tier int16
		Kind int16
	}
	q := `SELECT id, name, tier, kind FROM catalog_tag ` + whereClause(where) + ` ORDER BY id ASC LIMIT ?`
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return dto.PublicTagsListData{}, err
	}

	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	counts, err := s.workCountsFor(ctx, tagWorkEdge, ids, f.NSFW)
	if err != nil {
		return dto.PublicTagsListData{}, err
	}
	out := dto.PublicTagsListData{Items: make([]dto.PublicTagListItem, len(rows))}
	for i, r := range rows {
		out.Items[i] = dto.PublicTagListItem{
			ID: r.ID, Name: r.Name, Tier: tagTierKey(r.Tier), Kind: tagKindKey(r.Kind), WorkCount: counts[r.ID],
		}
	}
	if out.Total, err = s.taxonomyTotal(ctx, "catalog_tag", filterWhere, filterArgs); err != nil {
		return dto.PublicTagsListData{}, err
	}
	out.NextCursor = taxonomyNextCursor(taxonomyLaneTags, ids, limit)
	return out, nil
}

// EnginesList serves the keyset engine browse lane (id ASC). The facet fits in
// one page today; it is keyset-paged anyway so the three taxonomy lanes stay
// isomorphic and growth needs no contract change.
func (s *PublicService) EnginesList(ctx context.Context, f EnginesListFilter, cursor string, limit int) (dto.PublicEnginesListData, error) {
	cur, err := decodePublicCursor(cursor, taxonomyLaneEngines)
	if err != nil {
		return dto.PublicEnginesListData{}, err
	}
	limit = clampBrowseLimit(limit)

	var where []string
	var args []any
	if cur.ID > 0 {
		where = append(where, "id > ?")
		args = append(args, cur.ID)
	}
	args = append(args, limit)

	var rows []struct {
		ID          int64
		Name        string
		Description string
		Aliases     datatypes.JSON
	}
	q := `SELECT id, name, description, aliases FROM catalog_engine ` + whereClause(where) + ` ORDER BY id ASC LIMIT ?`
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return dto.PublicEnginesListData{}, err
	}

	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	counts, err := s.workCountsFor(ctx, engineWorkEdge, ids, f.NSFW)
	if err != nil {
		return dto.PublicEnginesListData{}, err
	}
	out := dto.PublicEnginesListData{Items: make([]dto.PublicEngineListItem, len(rows))}
	for i, r := range rows {
		out.Items[i] = dto.PublicEngineListItem{
			ID: r.ID, Name: r.Name, WorkCount: counts[r.ID],
			Description: r.Description, Aliases: engineAliases(r.Aliases),
		}
	}
	if out.Total, err = s.taxonomyTotal(ctx, "catalog_engine", nil, nil); err != nil {
		return dto.PublicEnginesListData{}, err
	}
	out.NextCursor = taxonomyNextCursor(taxonomyLaneEngines, ids, limit)
	return out, nil
}

// EngineDetail projects one engine (GET /v1/catalog/engines/{id}).
// found=false → 404 on an unknown id. refs[] rides the generic exact-only
// entity loader, so A2-0's wiki eid anchors surface with no extra plumbing.
func (s *PublicService) EngineDetail(ctx context.Context, id int64, nsfw bool) (dto.PublicEngine, bool, error) {
	var head struct {
		ID          int64
		Name        string
		Description string
		Aliases     datatypes.JSON
	}
	if err := s.db.WithContext(ctx).Raw(
		`SELECT id, name, description, aliases FROM catalog_engine WHERE id = ?`, id).Scan(&head).Error; err != nil {
		return dto.PublicEngine{}, false, err
	}
	if head.ID == 0 {
		return dto.PublicEngine{}, false, nil // identity PK starts at 1 → 0 = miss
	}
	counts, err := s.workCountsFor(ctx, engineWorkEdge, []int64{id}, nsfw)
	if err != nil {
		return dto.PublicEngine{}, false, err
	}
	rec := dto.PublicEngine{
		ID: head.ID, Name: head.Name, WorkCount: counts[id],
		Description: head.Description, Aliases: engineAliases(head.Aliases),
	}
	if rec.Refs, err = s.entityRefs(ctx, model.EntityTypeEngine, id); err != nil {
		return dto.PublicEngine{}, false, err
	}
	return rec, true, nil
}

// The three taxonomy→work edge fragments workCountsFor plugs in. FIXED
// internal SQL — never caller input — and each must expose exactly the two
// columns the helper joins on: key_id (the taxonomy id) and work_id. They are
// flat subqueries, so the planner pulls them up and the key_id IN (...) probe
// still lands on the edge tables' own reverse indexes.
const (
	labelWorkEdge  = `(SELECT label_id AS key_id, work_id FROM catalog_work_label) e`
	engineWorkEdge = `(SELECT engine_id AS key_id, work_id FROM catalog_work_engine) e`
	// A canonical tag reaches works through the source-name map, exactly as the
	// works list's tag_id filter does.
	tagWorkEdge = `(SELECT m.tag_id AS key_id, wt.work_id
		FROM catalog_work_tag wt
		JOIN catalog_tag_source_map m ON m.source_id = wt.source_id AND m.source_name = wt.name) e`
)

// workCountsFor batch-counts, per taxonomy id, the works a caller with this
// nsfw setting can actually reach through the matching works-list filter. ONE
// GROUP BY for the whole page (never per row); ids with no visible work are
// simply absent from the map, which renders as 0.
//
// count(DISTINCT work_id) is required, not decorative: catalog_work_label is
// keyed (work_id, label_id, kind) so one label may hold several edges to the
// same work, and several source tags may map onto one canonical tag.
func (s *PublicService) workCountsFor(ctx context.Context, edge string, ids []int64, nsfw bool) (map[int64]int, error) {
	out := make(map[int64]int, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// The works-list population predicate, verbatim — this equality IS the
	// contract (count == what works?<filter>= pages through).
	where := []string{"e.key_id IN ?", "w.deleted_at IS NULL", "w.status = ?", "w.medium_id = ?"}
	args := []any{ids, model.WorkStatusLive, galgameMediumID}
	if !nsfw {
		where = append(where, "w.content_rating <> ?")
		args = append(args, model.ContentRatingR18)
	}
	var rows []struct {
		KeyID int64 `gorm:"column:key_id"`
		N     int   `gorm:"column:n"`
	}
	q := `SELECT e.key_id, count(DISTINCT e.work_id) AS n FROM ` + edge +
		` JOIN catalog_work w ON w.id = e.work_id WHERE ` + strings.Join(where, " AND ") + ` GROUP BY e.key_id`
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.KeyID] = r.N
	}
	return out, nil
}

// taxonomyTotal counts the WHOLE filtered set of a browse lane (A2-1e) — the
// same predicate that produced the page, MINUS the cursor clause, so paging a
// lane to exhaustion collects exactly `total` rows.
//
// table is FIXED internal SQL (never caller input) and where holds only the
// lane's own filter fragments; every caller value stays a bound argument.
//
// Deliberately NOT nsfw-aware: a label / tag / engine row is an identity, and
// the nsfw gate on this face only ever governs CONTENT — which on these lanes
// means the per-row work_count, not whether the row itself exists.
func (s *PublicService) taxonomyTotal(ctx context.Context, table string, where []string, args []any) (int64, error) {
	var total int64
	q := `SELECT count(*) FROM ` + table + ` ` + whereClause(where)
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// engineAliases decodes catalog_engine.aliases (a jsonb string array) to the
// wire shape. A NULL / malformed / non-array value yields [] rather than an
// error: the column is a display convenience, and one bad row must not 500 a
// whole browse page.
func engineAliases(raw datatypes.JSON) []string {
	out := []string{}
	if len(raw) == 0 {
		return out
	}
	var vals []string
	if err := json.Unmarshal(raw, &vals); err != nil {
		return out
	}
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// clampBrowseLimit is the defensive service-side clamp shared by the secondary
// browse lanes (the three taxonomy lanes, the three calendar buckets) — the
// handler is the wire authority (it 400s a bad limit). Same numbers as the
// works list, and the same "clamp at the ceiling, never reset to default" rule
// so both layers agree on what an over-max limit means.
func clampBrowseLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

// taxonomyNextCursor mints the id-lane cursor for a full page, nil on the last
// one (the works-list convention: a short page ends the walk).
func taxonomyNextCursor(lane string, ids []int64, limit int) *string {
	if len(ids) < limit {
		return nil
	}
	c := encodePublicCursor(publicCursor{Sort: lane, ID: ids[len(ids)-1]})
	return &c
}

// whereClause renders an optional predicate list ("" when there is none), so
// the unfiltered first page of a lane needs no WHERE at all.
func whereClause(where []string) string {
	if len(where) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(where, " AND ")
}
