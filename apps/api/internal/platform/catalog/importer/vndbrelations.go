package importer

import (
	"api/internal/platform/catalog/model"

	"gorm.io/gorm/clause"
)

// VNDB work relations (doc 77 REL1, doc 17 R4). VNDB stores vn↔vn relations
// from BOTH endpoints' perspective: a row (id, vid, relation) reads "vid is-a
// {relation} of id" (verified empirically — v4/CLANNAD --seq--> v12/智代アフター
// means Tomoyo After is the sequel of CLANNAD; v7771/WhiteAlbum2 --fan-->
// v16493/白色相簿2 Mini After Story means Mini After Story is the fandisc of
// White Album 2). Every pair is stored twice: with the inverse relation
// (seq↔preq, side↔par, fan↔orig) or twice symmetrically (set/alt/char/ser). The
// paired rows fold to ONE directed catalog edge; ON CONFLICT dedups the mirror.
// We import only relations where BOTH vns carry an exact vndb work anchor
// (source 2) — a vn without a catalog work is excluded by construction.
//
// Direction and symmetric-normalization follow the exact conventions surveyed
// on the pre-existing edges (bangumirelations.go): symmetric types normalize
// a<b; directed types put the DERIVED/later work in a. Because the VNDB and
// Bangumi conventions are aligned, a pair asserted by both sources — even in
// mutually-inverse directions — converges on the SAME (a, b, type) and is
// deduped by the composite PK, so the two lanes never double-write an edge.

// Catalog work relation type ids used by the VNDB vocabulary but absent from
// the Bangumi game-domain wave's set (bangumirelations.go defines the shared
// ones — relSequelOf/relSideStoryOf/relSameSetting/… — which this file reuses).
const (
	relFandiscOf  int64 = 4
	relSameSeries int64 = 7
)

// vndbVNRelations pins the 10 VNDB relation kinds to catalog work relations
// (reusing bgmRelSpec, the generic {typeID, symmetric, flip} shape). Semantics
// are VNDB's documented relation vocabulary (never guessed). flip mirrors
// bangumirelations.go with id↔subject and vid↔related:
//   - default (flip=false): a=work(id),  b=work(vid)
//   - flip=true:            a=work(vid), b=work(id)
var vndbVNRelations = map[string]bgmRelSpec{
	// sequel/prequel: vid is sequel of id → vid (later) sequel_of id; vid is
	// prequel of id → id (later) sequel_of vid.
	"seq":  {typeID: relSequelOf, flip: true},
	"preq": {typeID: relSequelOf, flip: false},
	// side-story/parent: vid is side story of id → vid side_story_of id; vid is
	// parent story of id → id side_story_of vid.
	"side": {typeID: relSideStoryOf, flip: true},
	"par":  {typeID: relSideStoryOf, flip: false},
	// fandisc/original: vid is fandisc of id → vid (the fandisc) fandisc_of id;
	// vid is original game of id → id (the fandisc) fandisc_of vid.
	"fan":  {typeID: relFandiscOf, flip: true},
	"orig": {typeID: relFandiscOf, flip: false},
	// symmetric families (normalized a<b).
	"set":  {typeID: relSameSetting, symmetric: true},        // same setting
	"alt":  {typeID: relAlternativeVersion, symmetric: true}, // alternative version
	"char": {typeID: relSharesCharacter, symmetric: true},    // shares characters
	"ser":  {typeID: relSameSeries, symmetric: true},         // same series
}

// VNDBRelationStats is the wave tally (mirrors BangumiRelationStats).
type VNDBRelationStats struct {
	TotalRows         int
	Mapped            int // relation kind is a known catalog type (pre-anchor)
	Edges             int // NEW distinct edges this run
	EdgesWritten      int // actual inserts (== Edges on --run; 0 on dry)
	AlreadyInDB       int // edge already present (idempotent / cross-source convergence)
	InverseFolded     int // a mirror/duplicate row this run already produced the edge
	SkippedUnmapped   int // relation kind not in vndbVNRelations (unexpected — for 拍板)
	SkippedUnanchored int // one/both vns lack an exact vndb work anchor
	SkippedSelf       int // both ends resolve to the same work
}

// RunVNDBRelations imports the VNDB vn↔vn relations that connect two
// exact-anchored catalog works. Idempotent (ON CONFLICT DO NOTHING on the
// composite PK); dry by default. Existing edges (incl. the Bangumi lane's, when
// run after it) are read up-front so a re-run and cross-source overlaps are
// counted, never re-inserted or modified.
func (im *Importer) RunVNDBRelations() (VNDBRelationStats, error) {
	var st VNDBRelationStats

	anchor, err := im.loadVNDBAnchors(model.EntityTypeWork)
	if err != nil {
		return st, err
	}
	existing, err := im.loadExistingWorkRelations()
	if err != nil {
		return st, err
	}

	var rows []struct {
		ID  string `gorm:"column:id"`
		VID string `gorm:"column:vid"`
		Rel string `gorm:"column:relation"`
	}
	if err := im.catalog.Raw(`SELECT id, vid, relation FROM src_vndb.vn_relations`).Scan(&rows).Error; err != nil {
		return st, err
	}
	st.TotalRows = len(rows)

	src := vndbSource
	seen := make(map[[3]int64]struct{})
	var edges []model.CatalogWorkRelation
	for _, r := range rows {
		spec, ok := vndbVNRelations[r.Rel]
		if !ok {
			st.SkippedUnmapped++
			continue
		}
		st.Mapped++
		wID, okID := anchor[r.ID]
		wVID, okVID := anchor[r.VID]
		if !okID || !okVID {
			st.SkippedUnanchored++
			continue
		}
		if wID == wVID {
			st.SkippedSelf++
			continue
		}
		a, b := wID, wVID
		switch {
		case spec.symmetric:
			if a > b {
				a, b = b, a
			}
		case spec.flip:
			a, b = wVID, wID
		}
		key := [3]int64{a, b, spec.typeID}
		if _, inDB := existing[key]; inDB {
			st.AlreadyInDB++
			continue
		}
		if _, dup := seen[key]; dup {
			st.InverseFolded++
			continue
		}
		seen[key] = struct{}{}
		edges = append(edges, model.CatalogWorkRelation{
			AWorkID: a, BWorkID: b, RelationTypeID: spec.typeID, SourceID: &src,
		})
	}
	// --limit caps the plan (a rehearsal small-sample aid); edges are built in a
	// stable src-row order so the sample is deterministic.
	if im.limit > 0 && len(edges) > im.limit {
		edges = edges[:im.limit]
	}
	st.Edges = len(edges)

	if im.dryRun || len(edges) == 0 {
		return st, nil
	}
	res := im.catalog.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&edges, 1000)
	if res.Error != nil {
		return st, res.Error
	}
	st.EdgesWritten = int(res.RowsAffected)
	st.AlreadyInDB += st.Edges - st.EdgesWritten // any lost to a concurrent writer
	// Edges here are the planned-NEW set (already-stored pairs were filtered out
	// above), so both endpoints of each one genuinely changed. A re-run plans no
	// edges at all and returns before this point, touching nothing.
	if err := touchWorks(im.catalog, relationEndpoints(edges)); err != nil {
		return st, err
	}
	return st, nil
}
