package importer

import (
	"api/internal/platform/catalog/model"

	"gorm.io/gorm/clause"
)

// erogamescape work relations (doc 101 REL2). game_relations stores ONE row
// per directed assertion: (game_subject, game_object, kind) reads "subject is
// the {kind} of object" — the subject is the DERIVED/later work (verified
// empirically on title+date samples: わんもあ@ぴぃしぃず(2005) fandisk-of
// Peace@Pieces(2004), 水夏弐律(2011) sequel-of 水夏(2001), …), which aligns
// with the REL1 convention (derived work in a) with no flipping needed.
//
// Only relations whose BOTH endpoints carry an exact EG work anchor (source
// 5) are imported. bundling (store SKU packaging, not narrative), apend (DLC
// — the vocabulary has no append_of and we never guess) and free (ambiguous)
// are skipped BY DESIGN and counted for the report.
var egGameRelations = map[string]bgmRelSpec{
	"sequel":     {typeID: relSequelOf},                            // subject sequel_of object
	"fandisk":    {typeID: relFandiscOf},                           // subject fandisc_of object
	"remake":     {typeID: relRemakeOf},                            // subject remake_of object
	"spinoff":    {typeID: relSideStoryOf},                         // subject side_story_of object
	"transplant": {typeID: relAlternativeVersion, symmetric: true}, // same work, another platform's edition
}

// egSkippedByDesign names the kinds deliberately not mapped (doc 101 裁定 2).
var egSkippedByDesign = map[string]bool{"bundling": true, "apend": true, "free": true}

// EGRelationStats is the wave tally (mirrors VNDBRelationStats + the
// by-design bucket).
type EGRelationStats struct {
	TotalRows         int
	Mapped            int
	Edges             int // NEW distinct edges this run
	EdgesWritten      int
	AlreadyInDB       int // cross-source convergence (vndb/bgm asserted the pair first) / re-run
	Folded            int // duplicate src rows folding to an already-planned edge
	SkippedByDesign   int // bundling / apend / free (doc 101 裁定 2)
	SkippedUnmapped   int // unknown kind (unexpected — for 拍板)
	SkippedUnanchored int // one/both games lack an exact EG work anchor
	SkippedSelf       int // both ends resolve to the same work
}

// RunEGRelations imports the erogamescape game↔game relations that connect two
// exact-anchored catalog works. Idempotent (ON CONFLICT DO NOTHING on the
// composite PK); dry by default.
func (im *Importer) RunEGRelations() (EGRelationStats, error) {
	var st EGRelationStats

	anchor, err := im.loadEGWorkAnchors()
	if err != nil {
		return st, err
	}
	existing, err := im.loadExistingWorkRelations()
	if err != nil {
		return st, err
	}

	var rows []struct {
		Subject string `gorm:"column:subject"`
		Object  string `gorm:"column:object"`
		Kind    string `gorm:"column:kind"`
	}
	if err := im.eg.Raw(`SELECT game_subject::text AS subject, game_object::text AS object,
		raw::json->>'kind' AS kind FROM game_relations ORDER BY pk`).Scan(&rows).Error; err != nil {
		return st, err
	}
	st.TotalRows = len(rows)

	src := egSource
	seen := make(map[[3]int64]struct{})
	var edges []model.CatalogWorkRelation
	for _, r := range rows {
		spec, ok := egGameRelations[r.Kind]
		if !ok {
			if egSkippedByDesign[r.Kind] {
				st.SkippedByDesign++
			} else {
				st.SkippedUnmapped++
			}
			continue
		}
		st.Mapped++
		wSub, okSub := anchor[r.Subject]
		wObj, okObj := anchor[r.Object]
		if !okSub || !okObj {
			st.SkippedUnanchored++
			continue
		}
		if wSub == wObj {
			st.SkippedSelf++
			continue
		}
		a, b := wSub, wObj // derived work in a — EG's subject IS the derived work
		if spec.symmetric && a > b {
			a, b = b, a
		}
		key := [3]int64{a, b, spec.typeID}
		if _, inDB := existing[key]; inDB {
			st.AlreadyInDB++
			continue
		}
		if _, dup := seen[key]; dup {
			st.Folded++
			continue
		}
		seen[key] = struct{}{}
		edges = append(edges, model.CatalogWorkRelation{
			AWorkID: a, BWorkID: b, RelationTypeID: spec.typeID, SourceID: &src,
		})
	}
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
	st.AlreadyInDB += st.Edges - st.EdgesWritten
	// Edges here are the planned-NEW set (already-stored pairs were filtered out
	// above), so both endpoints of each one genuinely changed. A re-run plans no
	// edges at all and returns before this point, touching nothing.
	if err := touchWorks(im.catalog, relationEndpoints(edges)); err != nil {
		return st, err
	}
	return st, nil
}

// loadEGWorkAnchors maps EG game id (text) → catalog work id over the exact
// EG work anchors.
func (im *Importer) loadEGWorkAnchors() (map[string]int64, error) {
	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
	}
	if err := im.catalog.Raw(`
		SELECT external_id, entity_id FROM catalog_external_ref
		WHERE entity_type = ? AND source_id = ? AND link_kind = ?`,
		model.EntityTypeWork, egSource, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(rows))
	for _, r := range rows {
		m[r.ExternalID] = r.EntityID
	}
	return m, nil
}
