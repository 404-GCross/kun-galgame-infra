package wikirescue

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"
)

// galgameTagCategorySexual is the wiki tag category VNDB's `ero` maps to (the
// sync writes cont→content, ero→sexual, tech→technical). It is the sole input of
// both the per-edge `sexual` flag and the canonical vocabulary's tag-level one —
// service.galgameTagCategorySexual, kept in step with this constant.
const galgameTagCategorySexual = "sexual"

// tagEdgeKey identifies one catalog_work_tag row: the table's unique key
// uq_catalog_work_tag(work_id, name, source_id).
type tagEdgeKey struct {
	workID int64
	name   string
	srcID  int16
}

// tagEdgeVal is the value set step r owns on an edge. count is always 0 (the
// wiki tag layer has NO vote field anywhere — surveyed), so it is not a variable,
// but it stays in the diff so a foreign non-zero count can never survive inside
// the scope unnoticed.
type tagEdgeVal struct {
	count   int
	spoiler int16
	sexual  bool
}

// stepTagMirror mirrors the CLAIMED works' wiki tag layer into catalog_work_tag,
// and backfills the canonical vocabulary's tag-level sexual flag (step r,
// refs/proj/140 §2.3).
//
// The read face used to bridge galgame_tag_relation ⋈ galgame_tag for a claimed
// work (service.bridgeGalgameTags). This step materializes that join verbatim:
//
//   - name = galgame_tag.name, verbatim. That string is the LOCALIZED display
//     name kungal renders (the VNDB sync resolves english→chinese through the
//     wiki's tagMap), which is precisely why the tag layer is worth mirroring
//     rather than re-deriving from VNDB: the curated zh names live only here.
//   - source_id = the edge's own `source` mapped through the bridge's
//     galgameMediaSourceKey ('' → galgame_wiki, 'vndb' → vndb), so a curated tag
//     and a synced one stay told apart.
//   - spoiler = LEAST(GREATEST(COALESCE(spoiler_level,0),0),2) — the bridge's
//     clamp, verbatim. The wiki column is a bigint with no CHECK and the public
//     contract promises a three-value vocabulary.
//   - sexual = (category = 'sexual').
//   - count = 0.
//
// The bridge's spoilerMax CEILING is NOT applied: a mirror has to hold the whole
// truth (spoiler=2 edges included) and the ceiling moves into the read face's
// native query, where it keeps behaving exactly as before per caller.
//
// OWNERSHIP SCOPE — the claimed works' source_id ∈ {galgame_wiki, vndb} rows.
// The bangumi / dlsite rows on those same works are the step-88 / T1 folksonomy
// importers' property (they are why the tags facet moved to a per-source XOR in
// the first place) and are a RED LINE: not read, not updated, not deleted.
//
// An edge whose `source` maps outside the scope (galgameMediaSourceKey also knows
// bangumi/upscale, which the relation table has never carried) is PARKED rather
// than written: writing it would file a wiki row into another owner's source lane.
func (r *Runner) stepTagMirror(ctx context.Context) (Stats, error) {
	st := Stats{Step: "r"}

	span, err := r.claimedSpan(ctx)
	if err != nil {
		return st, err
	}
	srcIDs, err := r.mediaSourceIDs(ctx)
	if err != nil {
		return st, err
	}
	vndbSrc := srcIDs[mediaSourceVNDB]
	inScope := map[int16]bool{r.wikiSrc: true, vndbSrc: true}

	var edges []struct {
		GalgameID int64  `gorm:"column:galgame_id"`
		Name      string `gorm:"column:name"`
		Source    string `gorm:"column:source"`
		Spoiler   int16  `gorm:"column:spoiler"`
		Category  string `gorm:"column:category"`
	}
	// The bridge's SQL verbatim, minus the spoiler ceiling (see the doc comment).
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT r.galgame_id, t.name, COALESCE(r.source, '') AS source,
			LEAST(GREATEST(COALESCE(r.spoiler_level, 0), 0), 2)::smallint AS spoiler,
			t.category
		 FROM galgame_tag_relation r
		 JOIN galgame_tag t ON t.id = r.tag_id
		 ORDER BY r.galgame_id, t.name`).Scan(&edges).Error; err != nil {
		return st, fmt.Errorf("read galgame tag edges: %w", err)
	}
	st.Source = len(edges)

	type parkedTagEdge struct {
		GalgameID int64  `json:"galgame_id"`
		Name      string `json:"name"`
		Source    string `json:"source"`
	}
	parked := make([]parkedTagEdge, 0)
	desired := newMirrorDesired[tagEdgeKey, tagEdgeVal](len(edges))
	for _, e := range edges {
		workID, ok := span.byGalgame[e.GalgameID]
		if !ok {
			continue
		}
		// mediaSourceID is the bridge's galgameMediaSourceKey lookup with the same
		// galgame_wiki fallback for an unrecognized value.
		srcID := mediaSourceID(srcIDs, e.Source)
		if !inScope[srcID] {
			parked = append(parked, parkedTagEdge{GalgameID: e.GalgameID, Name: e.Name, Source: e.Source})
			continue
		}
		// put keeps the first row for a repeated key, matching the bridge's
		// first-wins append order (the key cannot repeat today: galgame_tag.name is
		// UNIQUE and (galgame_id, tag_id) is the relation PK).
		desired.put(tagEdgeKey{workID: workID, name: e.Name, srcID: srcID},
			tagEdgeVal{count: 0, spoiler: e.Spoiler, sexual: e.Category == galgameTagCategorySexual})
	}
	st.Anchored = desired.len()
	st.Parked = len(parked)
	if err := r.park("r-tag-edges-out-of-scope", parked); err != nil {
		return st, err
	}

	var scope []struct {
		ID       int64  `gorm:"column:id"`
		WorkID   int64  `gorm:"column:work_id"`
		Name     string `gorm:"column:name"`
		SourceID int16  `gorm:"column:source_id"`
		Count    int    `gorm:"column:count"`
		Spoiler  int16  `gorm:"column:spoiler"`
		Sexual   bool   `gorm:"column:sexual"`
	}
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT t.id, t.work_id, t.name, t.source_id, t.count, t.spoiler, t.sexual FROM catalog_work_tag t
		 `+claimedScopeJoin+` WHERE t.source_id IN ?`,
		siteGalgameWiki, []int16{r.wikiSrc, vndbSrc}).Scan(&scope).Error; err != nil {
		return st, fmt.Errorf("read catalog_work_tag scope: %w", err)
	}
	existing := make(map[tagEdgeKey]mirrorExisting[tagEdgeVal], len(scope))
	for _, x := range scope {
		existing[tagEdgeKey{workID: x.WorkID, name: x.Name, srcID: x.SourceID}] =
			mirrorExisting[tagEdgeVal]{id: x.ID, val: tagEdgeVal{count: x.Count, spoiler: x.Spoiler, sexual: x.Sexual}}
	}

	spec := mirrorSpec[tagEdgeKey, tagEdgeVal]{
		table:      "catalog_work_tag",
		insertCols: []string{"work_id", "name", "count", "source_id", "spoiler", "sexual", "created_at", "updated_at"},
		rowOf: func(k tagEdgeKey, v tagEdgeVal) []any {
			return []any{k.workID, k.name, v.count, k.srcID, v.spoiler, v.sexual, r.now, r.now}
		},
		updateCols: []string{"count", "spoiler", "sexual", "updated_at"},
		updateArgs: func(v tagEdgeVal) []any { return []any{v.count, v.spoiler, v.sexual, r.now} },
		workOf:     func(k tagEdgeKey) int64 { return k.workID },
		same:       func(a, b tagEdgeVal) bool { return a == b },
	}
	ledger := newMirrorLedger()
	if err := runMirror(ctx, r, ledger, spec, desired, existing); err != nil {
		return st, err
	}
	ledger.into(&st)

	vocab, err := r.mirrorTagVocabularySexual(ctx)
	if err != nil {
		return st, err
	}
	st.Vocab = vocab
	st.Note = fmt.Sprintf("scope=claimed×{galgame_wiki,vndb}; bgm/dlsite rows untouched; catalog_tag.sexual rows corrected=%d", vocab)
	return st, nil
}

// mirrorTagVocabularySexual brings catalog_tag.sexual in step with the wiki
// vocabulary, resolving each canonical tag through its A2-0 identity anchor
// (catalog_external_ref entity_type=tag, source=galgame_wiki, link_kind=exact,
// external_id = the wiki tag id) — the SAME derivation service.tagSexualFor
// computes at read time today, written into the column the flipped face reads.
//
// Resolving by ID and not by the vndb source NAME is deliberate and inherited:
// both resolve the same tags today, but a name join silently drops a tag the
// moment either side is renamed. The ref's external_id is compared as TEXT
// because that table is a mixed key space — casting it to bigint would turn one
// foreign non-numeric row into a query-wide error; here the comparison happens in
// Go, over the wiki ids rendered as text, which has the same property.
//
// The WHOLE column is the scope: a tag that is not sexual-category (or has no
// wiki anchor at all) must read false, which is exactly what tagSexualFor's
// absent-from-the-map default renders today. Both directions are corrected, so a
// converged re-run writes nothing. The two families stay on their own pools (the
// read face can join them because they share a physical database; this package
// does not rely on that).
func (r *Runner) mirrorTagVocabularySexual(ctx context.Context) (int, error) {
	var sexualWikiIDs []string
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id::text FROM galgame_tag WHERE category = ?`, galgameTagCategorySexual,
	).Scan(&sexualWikiIDs).Error; err != nil {
		return 0, fmt.Errorf("read sexual wiki tags: %w", err)
	}
	var want []int64
	if len(sexualWikiIDs) > 0 {
		if err := r.catalog.WithContext(ctx).Raw(
			`SELECT DISTINCT ref.entity_id FROM catalog_external_ref ref
			 JOIN catalog_source src ON src.id = ref.source_id AND src.key = ?
			 WHERE ref.entity_type = ? AND ref.link_kind = ? AND ref.external_id IN ?`,
			sourceKeyWiki, model.EntityTypeTag, model.LinkKindExact, sexualWikiIDs,
		).Scan(&want).Error; err != nil {
			return 0, fmt.Errorf("resolve sexual canonical tags: %w", err)
		}
	}

	// The vocabulary is small (a few thousand rows), so the diff runs in Go: two
	// id lists beat two mutually-negating SQL predicates, and the forecast then
	// counts EXACTLY the rows the write would touch.
	wantSexual := make(map[int64]bool, len(want))
	for _, id := range want {
		wantSexual[id] = true
	}
	var current []struct {
		ID     int64 `gorm:"column:id"`
		Sexual bool  `gorm:"column:sexual"`
	}
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT id, sexual FROM catalog_tag ORDER BY id`).Scan(&current).Error; err != nil {
		return 0, fmt.Errorf("read catalog_tag.sexual: %w", err)
	}
	var promote, demote []int64
	for _, t := range current {
		switch {
		case wantSexual[t.ID] && !t.Sexual:
			promote = append(promote, t.ID)
		case !wantSexual[t.ID] && t.Sexual:
			demote = append(demote, t.ID)
		}
	}
	changed := len(promote) + len(demote)
	if !r.opts.Apply {
		return changed, nil
	}
	for _, arm := range []struct {
		ids []int64
		val bool
	}{{promote, true}, {demote, false}} {
		if len(arm.ids) == 0 {
			continue
		}
		if err := r.catalog.WithContext(ctx).Exec(
			`UPDATE catalog_tag SET sexual = ?, updated_at = ? WHERE id IN ?`,
			arm.val, r.now, arm.ids).Error; err != nil {
			return 0, fmt.Errorf("update catalog_tag.sexual: %w", err)
		}
	}
	return changed, nil
}
