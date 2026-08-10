// public_taxonomy.go — the taxonomy read faces (A2-1b, refs/proj/126 D3): the
// labels / tags / engines browse lanes plus the engine detail record. Pure
// add-only DB read surface; the keyset / limit / cursor posture is the works
// list's verbatim so every /v1/catalog browse lane pages identically.
//
// work_count is the wave's load-bearing invariant. It is NSFW-AWARE: the number
// a caller gets is the number of works that same caller would actually page
// through via works?label_id=/tag_id=/engine_id=. The count query therefore
// reuses the works-list population predicate literally (LIVE galgame, not
// soft-deleted, r18 dropped unless nsfw, and — wave 146 — claim_state=live, the
// gate an entity page's member list passes) instead of counting edge rows. The
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
	taxonomyLaneSeries  = "series"
)

// LabelsListFilter is the label browse lane's request shape.
type LabelsListFilter struct {
	Kind     *int16 // model.LabelKind* — nil = every kind
	NSFW     bool
	HasWorks bool // true = only rows whose work_count (under this NSFW) is > 0
}

// TagsListFilter is the canonical-tag browse lane's request shape.
type TagsListFilter struct {
	Tier     *int16 // model.TagTier* — nil = every tier
	Kind     *int16 // model.TagKind* — nil = every kind
	NSFW     bool
	HasWorks bool // true = only rows whose work_count (under this NSFW) is > 0
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
	if f.HasWorks {
		pred, pargs := workExistsClause(labelWorkEdge, "catalog_label.id", f.NSFW)
		where = append(where, pred)
		args = append(args, pargs...)
	}
	// Snapshot the FILTER-only predicate before the cursor clause joins it: the
	// total counts the whole filtered set, not the tail after the cursor.
	filterWhere, filterArgs := append([]string(nil), where...), append([]any(nil), args...)
	if cur.ID > 0 {
		where = append(where, "id > ?")
		args = append(args, cur.ID)
	}
	args = append(args, limit+taxonomyOverFetch)

	var rows []struct {
		ID          int64
		DisplayName string
		Kind        int16
		LogoHash    string
	}
	q := `SELECT id, display_name, kind, logo_hash FROM catalog_label WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY id ASC LIMIT ?`
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return dto.PublicLabelsListData{}, err
	}

	rows, more := taxonomyTrim(rows, limit)
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	counts, err := s.workCountsFor(ctx, labelWorkEdge, ids, f.NSFW)
	if err != nil {
		return dto.PublicLabelsListData{}, err
	}
	related, err := s.labelsWithRelations(ctx, ids)
	if err != nil {
		return dto.PublicLabelsListData{}, err
	}
	out := dto.PublicLabelsListData{Items: make([]dto.PublicLabelListItem, len(rows))}
	for i, r := range rows {
		out.Items[i] = dto.PublicLabelListItem{
			ID: r.ID, DisplayName: r.DisplayName, Kind: labelKindKey(r.Kind), WorkCount: counts[r.ID],
			LogoHash: r.LogoHash, HasRelations: related[r.ID],
		}
	}
	if out.Total, err = s.taxonomyTotal(ctx, "catalog_label", filterWhere, filterArgs); err != nil {
		return dto.PublicLabelsListData{}, err
	}
	out.NextCursor = taxonomyNextCursor(taxonomyLaneLabels, ids, more)
	return out, nil
}

// labelsWithRelations reports, for one page of label ids, which of them have at
// least one corporate-family edge. One grouped query per page — the same shape
// as workCountsFor, and the reason the browse row can answer this at all
// without an N+1.
//
// It publishes a FLAG rather than the edges themselves on purpose. Only 2,139
// of 39,653 labels carry any relation (5.4%), so shipping relations[] on every
// row would join the whole page to serve a twentieth of it, on a lane that is
// deliberately thin. The flag is what a consumer actually needs from a list:
// which rows are worth a labels/{id}/relation-graph call. Without it, browsing
// twenty labels means twenty speculative calls to find the one family — the
// exact N+1 this closes. Same reasoning as has_nsfw on the series lane.
func (s *PublicService) labelsWithRelations(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		LabelID int64 `gorm:"column:label_id"`
	}
	if err := s.db.WithContext(ctx).Raw(
		`SELECT DISTINCT label_id FROM catalog_label_relation WHERE label_id IN ?`, ids,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.LabelID] = true
	}
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
	if f.HasWorks {
		pred, pargs := workExistsClause(tagWorkEdge, "catalog_tag.id", f.NSFW)
		where = append(where, pred)
		args = append(args, pargs...)
	}
	// Filter-only predicate for the total (see LabelsList).
	filterWhere, filterArgs := append([]string(nil), where...), append([]any(nil), args...)
	if cur.ID > 0 {
		where = append(where, "id > ?")
		args = append(args, cur.ID)
	}
	args = append(args, limit+taxonomyOverFetch)

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

	rows, more := taxonomyTrim(rows, limit)
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	counts, err := s.workCountsFor(ctx, tagWorkEdge, ids, f.NSFW)
	if err != nil {
		return dto.PublicTagsListData{}, err
	}
	sexual, err := s.tagSexualFor(ctx, ids)
	if err != nil {
		return dto.PublicTagsListData{}, err
	}
	out := dto.PublicTagsListData{Items: make([]dto.PublicTagListItem, len(rows))}
	for i, r := range rows {
		out.Items[i] = dto.PublicTagListItem{
			ID: r.ID, Name: r.Name, Tier: tagTierKey(r.Tier), Kind: tagKindKey(r.Kind),
			WorkCount: counts[r.ID], Sexual: sexual[r.ID],
		}
	}
	if out.Total, err = s.taxonomyTotal(ctx, "catalog_tag", filterWhere, filterArgs); err != nil {
		return dto.PublicTagsListData{}, err
	}
	out.NextCursor = taxonomyNextCursor(taxonomyLaneTags, ids, more)
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
	args = append(args, limit+taxonomyOverFetch)

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

	rows, more := taxonomyTrim(rows, limit)
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
	out.NextCursor = taxonomyNextCursor(taxonomyLaneEngines, ids, more)
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
// taxonomyLiveClaim is the claim gate every work_count on this face is counted
// behind (wave 146). It is a CONSTANT, not a parameter, and that is the point:
// the number beside a chip must be the number the reader gets by following it,
// and what a downstream entity page follows it with is
// works?<filter>=&claim_state=live — the spelling A2-R4 introduced precisely
// because DRAFT (unpublished) stubs and unclaimed registry rows were reaching
// public member lists. Counting them anyway is how a tag page came to promise
// works nobody can see: at the 2026-07-30 断面 the galgame medium held 10,927
// live claims beside 53,521 drafts and 17,560 unclaimed rows, so the ungated
// number ran ~6x high. Ruled by the track owner (the A2-R wave logged it as a
// candidate; 146 executes it).
//
// The nsfw axis is deliberately untouched by that ruling: it stays the caller's
// (doc 106 §23 "identity is not content" — nsfw governs the per-row count,
// never whether the taxonomy row itself exists).
var taxonomyLiveClaim = []string{model.ClaimStateKeyLive}

// taxonomyEdge is one taxonomy → work edge: the SQL projecting (key_id,
// work_id) pairs, plus — for the one edge that needs it — the table holding
// those counts already computed.
type taxonomyEdge struct {
	sql string
	// rollup names the table workCountsWithNSFW reads instead of aggregating
	// this edge live. Empty is the default and the honest one: the count comes
	// out of the same population the caller is about to page through, so it
	// cannot be wrong. Only an edge whose live aggregate is genuinely too
	// expensive earns a rollup — see model.CatalogTagWorkCount for why exactly
	// one of these does.
	rollup string
}

var (
	labelWorkEdge  = taxonomyEdge{sql: `(SELECT label_id AS key_id, work_id FROM catalog_work_label) e`}
	engineWorkEdge = taxonomyEdge{sql: `(SELECT engine_id AS key_id, work_id FROM catalog_work_engine) e`}
	seriesWorkEdge = taxonomyEdge{sql: `(SELECT series_id AS key_id, work_id FROM catalog_series_member) e`}
	// A canonical tag reaches works through the source-name map, exactly as the
	// works list's tag_id filter does — and that indirection is what makes this
	// the one edge counted from a rollup rather than live.
	tagWorkEdge = taxonomyEdge{
		sql: `(SELECT m.tag_id AS key_id, wt.work_id
		FROM catalog_work_tag wt
		JOIN catalog_tag_source_map m ON m.source_id = wt.source_id AND m.source_name = wt.name) e`,
		rollup: "catalog_tag_work_count",
	}
)

// workCountsFor batch-counts, per taxonomy id, the works a caller with this
// nsfw setting can actually reach through the matching works-list filter. ONE
// GROUP BY for the whole page (never per row); ids with no visible work are
// simply absent from the map, which renders as 0.
//
// count(DISTINCT work_id) is required, not decorative: catalog_work_label is
// keyed (work_id, label_id, kind) so one label may hold several edges to the
// same work, and several source tags may map onto one canonical tag.
func (s *PublicService) workCountsFor(ctx context.Context, edge taxonomyEdge, ids []int64, nsfw bool) (map[int64]int, error) {
	counts, _, err := s.workCountsWithNSFW(ctx, edge, ids, nsfw)
	return counts, err
}

// workCountsWithNSFW returns the visible count AND, per id, how many of that
// id's works carry content_limit = nsfw — the second number computed WITHOUT
// the caller's nsfw filter.
//
// TWO DIFFERENT AXES, on purpose (the vocabulary note on model.DisplayLimitKey):
//
//   - `counts` obeys the caller's `nsfw` query parameter, which has always
//     gated the AGE axis (content_rating). It answers "how many works do I get
//     if I follow this chip", so it must not change meaning here.
//   - `nsfwWorks` reads the DISPLAY axis (content_limit, i.e. the editorial
//     display_nsfw flag for claimed rows and the age fallback for bodyless
//     ones), compiled by displayLimitWhere — never re-derived, exactly as the
//     claim gate is never re-derived. It answers "is any material under this
//     row unsafe to render", which is what a badge is for.
//
// Mapping the age axis onto the display question would be the 5,568-work
// incident again, in reverse and larger: on the current registry 61,690 live
// claimed galgame works are r18 while only 13,664 are editorially nsfw, so an
// age-derived badge would over-mark by 4.5x — 48,299 works whose wiki material
// an editor judged safe to show.
//
// The asymmetry of ignoring the caller's nsfw setting for the second tally is
// the point: an sfw caller cannot learn "this row leads somewhere you are being
// shielded from" from a number that already subtracted those works.
//
// Both come out of ONE aggregate over ONE population, via FILTER rather than a
// second query, so the two can never disagree about which works they were
// counting — the invariant that matters when a consumer renders them together.
func (s *PublicService) workCountsWithNSFW(ctx context.Context, edge taxonomyEdge, ids []int64, nsfw bool) (counts, nsfwWorks map[int64]int, err error) {
	if edge.rollup != "" {
		return s.workCountsFromRollup(ctx, edge, ids, nsfw)
	}
	return s.workCountsLive(ctx, edge, ids, nsfw)
}

// workCountsLive is the aggregate itself — workCountsWithNSFW's meaning,
// computed from the edge every time. It stays the single definition of what
// these numbers ARE: a rolled-up edge is refreshed by calling this with a nil
// id list, so the stored counts are by construction the ones this would have
// returned, and can only ever be out of date, never a different question.
//
// ids == nil means every key (refresh); a non-nil empty list means none.
func (s *PublicService) workCountsLive(ctx context.Context, edge taxonomyEdge, ids []int64, nsfw bool) (counts, nsfwWorks map[int64]int, err error) {
	counts, nsfwWorks = make(map[int64]int, len(ids)), make(map[int64]int, len(ids))
	if ids != nil && len(ids) == 0 {
		return counts, nsfwWorks, nil
	}
	// The visible-count FILTER carries the nsfw axis that used to sit in WHERE:
	// the population must stay whole so the display tally can see the very works
	// the caller is being shielded from.
	visible := "TRUE"
	var args []any
	if !nsfw {
		visible = "w.content_rating <> ?"
		args = append(args, model.ContentRatingR18)
	}
	nsfwPred, nsfwArgs := displayLimitWhere([]string{model.DisplayLimitKeyNSFW})
	args = append(args, nsfwArgs...)

	// The works-list population predicate, verbatim — this equality IS the
	// contract (count == what works?<filter>= pages through). The key filter is
	// the ONLY optional part: a refresh pass wants every key, and asking for
	// them by listing all of them would be the same query with a worse plan.
	where := []string{"w.deleted_at IS NULL", "w.status = ?", "w.medium_id = ?"}
	if ids != nil {
		where = append([]string{"e.key_id IN ?"}, where...)
		args = append(args, ids)
	}
	args = append(args, model.WorkStatusLive, galgameMediumID)
	// …and the CLAIM gate an entity page's member list passes (wave 146). Not a
	// second opinion about it: the predicate is compiled by the very same
	// claimStateWhere the works list compiles `claim_state=live` with, so the two
	// cannot drift by construction. See taxonomyLiveClaim for why live is THE
	// number here and not merely one of several a caller may ask for.
	pred, pargs := claimStateWhere(taxonomyLiveClaim)
	where = append(where, pred)
	args = append(args, pargs...)

	var rows []struct {
		KeyID int64 `gorm:"column:key_id"`
		N     int   `gorm:"column:n"`
		NNSFW int   `gorm:"column:n_nsfw"`
	}
	q := `SELECT e.key_id,
			count(DISTINCT e.work_id) FILTER (WHERE ` + visible + `) AS n,
			count(DISTINCT e.work_id) FILTER (WHERE ` + nsfwPred + `) AS n_nsfw
		FROM ` + edge.sql + ` JOIN catalog_work w ON w.id = e.work_id
		WHERE ` + strings.Join(where, " AND ") + ` GROUP BY e.key_id`
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	for _, r := range rows {
		if r.N > 0 {
			counts[r.KeyID] = r.N
		}
		if r.NNSFW > 0 {
			nsfwWorks[r.KeyID] = r.NNSFW
		}
	}
	return counts, nsfwWorks, nil
}

// workExistsClause compiles the has_works row filter: keep a taxonomy row only
// if at least one work the SAME caller can reach through the matching
// works?<filter>= call hangs off it. It is workCountsFor's predicate verbatim —
// correlated on the outer row instead of batched over a page — so "work_count
// > 0" and "row survives the filter" cannot drift apart. That shared predicate
// is nsfw-aware, which makes has_works nsfw-aware by construction: a label
// whose only works are r18 exists for an nsfw caller and vanishes for an sfw
// one, exactly like its count. outerID is FIXED internal SQL (a qualified id
// column), never caller input.
func workExistsClause(edge taxonomyEdge, outerID string, nsfw bool) (string, []any) {
	where := []string{"e.key_id = " + outerID, "w.deleted_at IS NULL", "w.status = ?", "w.medium_id = ?"}
	args := []any{model.WorkStatusLive, galgameMediumID}
	if !nsfw {
		where = append(where, "w.content_rating <> ?")
		args = append(args, model.ContentRatingR18)
	}
	pred, pargs := claimStateWhere(taxonomyLiveClaim)
	where = append(where, pred)
	args = append(args, pargs...)
	return `EXISTS (SELECT 1 FROM ` + edge.sql + ` JOIN catalog_work w ON w.id = e.work_id WHERE ` +
		strings.Join(where, " AND ") + `)`, args
}

// tagSexualFor batch-resolves the TAG-LEVEL sexual-category flag (A2-1f) for a
// set of canonical tag ids. Tags whose flag is false are simply absent from
// the map, which renders false — the same absent-means-false contract the old
// read-time derivation had.
//
// The flag is the catalog_tag.sexual column since the W1-pre nativization
// (refs/proj/140): what used to be derived at read time by joining the wiki
// vocabulary through the A2-0 identity anchors (catalog_external_ref
// entity_type=tag → galgame_tag.category = 'sexual') is written onto the
// canonical row by wikirescue step r, using THAT SAME derivation, and kept in
// step daily until the wiki family drops.
func (s *PublicService) tagSexualFor(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		TagID int64 `gorm:"column:id"`
	}
	if err := s.db.WithContext(ctx).Raw(
		`SELECT id FROM catalog_tag WHERE id IN ? AND sexual`, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.TagID] = true
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
// Deliberately NOT nsfw-aware on its own: a label / tag / engine row is an
// identity, and the nsfw gate on this face only ever governs CONTENT — which on
// these lanes means the per-row work_count, not whether the row itself exists.
// The one caller-chosen exception is has_works: opting into it puts the
// (nsfw-aware) existence predicate INTO the lane's filter, and this total
// follows that filter like any other, so page rows and total keep converging.
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

// taxonomyOverFetch is what each lane adds to its LIMIT. A page that comes back
// exactly full is ambiguous — it can equally be the last page — and all four
// lanes used to resolve that ambiguity by guessing "there is more", so walking
// a facet whose size is a multiple of the limit ended on a cursor that led to an
// empty page. Fetching one extra row turns the guess into evidence: the extra
// row exists only when a further page does.
const taxonomyOverFetch = 1

// taxonomyTrim drops the over-fetched row and reports whether it was there.
func taxonomyTrim[T any](rows []T, limit int) (page []T, more bool) {
	if len(rows) > limit {
		return rows[:limit], true
	}
	return rows, false
}

// taxonomyNextCursor mints the id-lane cursor for a full page, nil on the last
// one (the works-list convention: a short page ends the walk).
func taxonomyNextCursor(lane string, ids []int64, more bool) *string {
	if !more || len(ids) == 0 {
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
