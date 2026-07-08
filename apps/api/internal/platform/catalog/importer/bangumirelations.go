package importer

import (
	"strconv"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm/clause"
)

// Bangumi work relations (step 30, doc 17 R4). Bangumi stores subject↔subject
// relations from BOTH endpoints' perspective: a row (subject S, relation R,
// related T) reads "T is-a R of S" (verified empirically on known series, e.g.
// 戦女神ZERO --续集--> 戦女神VERITA: VERITA is the later work). We import only
// the game-domain (subject_type 4) relations where BOTH subjects carry an exact
// bangumi work anchor — so cross-media edges (game→anime/manga) are excluded by
// construction (the other end is unregistered → unanchored). The paired
// inverse relations fold to ONE directed catalog edge; ON CONFLICT dedups the
// mirror row.

// Catalog work relation type ids (seed, step 02 + step 30 additions). The
// cross-media adaptation_of (1) is not imported by the game-domain relation wave
// (see below); step 31's cross-media wave uses it.
const (
	relAdaptationOf       int64 = 1
	relSequelOf           int64 = 2
	relSideStoryOf        int64 = 3
	relCollects           int64 = 5
	relRemakeOf           int64 = 6
	relSameSetting        int64 = 8
	relCrossoverWith      int64 = 9
	relSharesCharacter    int64 = 10
	relAlternativeSetting int64 = 11
	relAlternativeVersion int64 = 12
)

// bgmRelSpec maps a Bangumi game-domain relation to a catalog work relation.
type bgmRelSpec struct {
	typeID int64
	// symmetric types normalize to a<b; directed types write the edge either as
	// (work(S) --> work(T)) [flip=false] or (work(T) --> work(S)) [flip=true],
	// chosen so an inverse pair folds to the same edge.
	symmetric bool
	flip      bool
}

// bgmGameRelations pins the 13 in-use game-domain relations to catalog types.
// Semantics are the bangumicommon/data/subject_relations.yml domain-4 defs
// (never guessed); direction from "T is-a R of S". Deliberately absent: 4099
// (其他, no edge) and the cross-media 改编(1)/DLC(4015) — the latter carry no
// both-anchored game↔game rows and adaptation is the cross-media case the step
// defers to the anime/manga registration waves.
var bgmGameRelations = map[int]bgmRelSpec{
	// prequel/sequel: T is sequel of S → T sequel_of S; T is prequel of S → S sequel_of T.
	4003: {typeID: relSequelOf, flip: true},
	4002: {typeID: relSequelOf, flip: false},
	// side-story/parent: T is side story of S → T side_story_of S; T is parent of S → S side_story_of T.
	4006: {typeID: relSideStoryOf, flip: true},
	4012: {typeID: relSideStoryOf, flip: false},
	// collection: T is a collected work of S → S collects T; T is the collection of S → T collects S.
	4019: {typeID: relCollects, flip: false},
	4018: {typeID: relCollects, flip: true},
	// version/remaster: T is a version of S (S the main) → T remake_of S; T is main version of S → S remake_of T.
	4016: {typeID: relRemakeOf, flip: true},
	4017: {typeID: relRemakeOf, flip: false},
	// symmetric families.
	4008: {typeID: relSameSetting, symmetric: true},        // same world, different characters
	4014: {typeID: relCrossoverWith, symmetric: true},      // collaboration
	4007: {typeID: relSharesCharacter, symmetric: true},    // same characters, no linked story
	4009: {typeID: relAlternativeSetting, symmetric: true}, // same characters, different world
	4010: {typeID: relAlternativeVersion, symmetric: true}, // same setting/characters, different rendition
}

// BangumiRelationStats is the wave tally.
type BangumiRelationStats struct {
	TotalRows         int
	Mapped            int // relation_type is a known catalog type (pre-anchor)
	Edges             int // NEW distinct edges written this run
	EdgesWritten      int // actual inserts (== Edges on --run; 0 on dry)
	AlreadyInDB       int // edge already present (idempotent re-run)
	InverseFolded     int // a mirror/duplicate row this run already produced the edge
	SkippedOther      int // 4099 or an unmapped relation_type (incl. cross-media 改编)
	SkippedUnanchored int // one/both ends lack an exact bangumi work anchor
	SkippedSelf       int // both ends resolve to the same work
}

// RunBangumiRelations imports the Bangumi game-domain subject relations that
// connect two exact-anchored catalog works. Idempotent (ON CONFLICT DO
// NOTHING on the composite PK); dry by default.
func (im *Importer) RunBangumiRelations() (BangumiRelationStats, error) {
	var st BangumiRelationStats

	anchor, err := im.loadBangumiWorkAnchors()
	if err != nil {
		return st, err
	}
	existing, err := im.loadExistingWorkRelations()
	if err != nil {
		return st, err
	}

	var rows []struct {
		SubjectID    int64 `gorm:"column:subject_id"`
		RelationType int   `gorm:"column:relation_type"`
		Related      int64 `gorm:"column:related_subject_id"`
	}
	if err := im.catalog.Raw(`SELECT subject_id, relation_type, related_subject_id
		FROM src_bangumi.subject_relation`).Scan(&rows).Error; err != nil {
		return st, err
	}
	st.TotalRows = len(rows)

	src := bangumiSource
	seen := make(map[[3]int64]struct{})
	var edges []model.CatalogWorkRelation
	for _, r := range rows {
		spec, ok := bgmGameRelations[r.RelationType]
		if !ok {
			st.SkippedOther++
			continue
		}
		st.Mapped++
		wS, okS := anchor[r.SubjectID]
		wT, okT := anchor[r.Related]
		if !okS || !okT {
			st.SkippedUnanchored++
			continue
		}
		if wS == wT {
			st.SkippedSelf++
			continue
		}
		a, b := wS, wT
		switch {
		case spec.symmetric:
			if a > b {
				a, b = b, a
			}
		case spec.flip:
			a, b = wT, wS
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
	return st, nil
}

// loadBangumiWorkAnchors maps bangumi subject_id → work id for every exact
// bangumi work anchor (the partial-unique index guarantees one work per
// subject).
func (im *Importer) loadBangumiWorkAnchors() (map[int64]int64, error) {
	var rows []struct {
		Ext string `gorm:"column:external_id"`
		Wid int64  `gorm:"column:entity_id"`
	}
	if err := im.catalog.Raw(`SELECT external_id, entity_id FROM catalog_external_ref
		WHERE source_id = ? AND entity_type = ? AND link_kind = ?`,
		bangumiSource, model.EntityTypeWork, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(rows))
	for _, r := range rows {
		sid, err := strconv.ParseInt(r.Ext, 10, 64)
		if err != nil {
			continue // non-numeric bangumi external id — skip
		}
		m[sid] = r.Wid
	}
	return m, nil
}

// loadExistingWorkRelations reads the current work-relation PKs so a re-run (or
// a mirror row) is counted, not re-inserted.
func (im *Importer) loadExistingWorkRelations() (map[[3]int64]struct{}, error) {
	var rows []struct {
		A int64 `gorm:"column:a_work_id"`
		B int64 `gorm:"column:b_work_id"`
		T int64 `gorm:"column:relation_type_id"`
	}
	if err := im.catalog.Raw(`SELECT a_work_id, b_work_id, relation_type_id FROM catalog_work_relation`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[[3]int64]struct{}, len(rows))
	for _, r := range rows {
		m[[3]int64{r.A, r.B, r.T}] = struct{}{}
	}
	return m, nil
}
