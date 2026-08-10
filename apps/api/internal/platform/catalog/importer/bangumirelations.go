package importer

import (
	"strconv"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm/clause"
)

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

type bgmRelSpec struct {
	typeID    int64
	symmetric bool
	flip      bool
}

var bgmGameRelations = map[int]bgmRelSpec{
	4003: {typeID: relSequelOf, flip: true},
	4002: {typeID: relSequelOf, flip: false},
	4006: {typeID: relSideStoryOf, flip: true},
	4012: {typeID: relSideStoryOf, flip: false},
	4019: {typeID: relCollects, flip: false},
	4018: {typeID: relCollects, flip: true},
	4016: {typeID: relRemakeOf, flip: true},
	4017: {typeID: relRemakeOf, flip: false},
	4008: {typeID: relSameSetting, symmetric: true},
	4014: {typeID: relCrossoverWith, symmetric: true},
	4007: {typeID: relSharesCharacter, symmetric: true},
	4009: {typeID: relAlternativeSetting, symmetric: true},
	4010: {typeID: relAlternativeVersion, symmetric: true},
}

type BangumiRelationStats struct {
	TotalRows         int
	Mapped            int
	Edges             int
	EdgesWritten      int
	AlreadyInDB       int
	InverseFolded     int
	SkippedOther      int
	SkippedUnanchored int
	SkippedSelf       int
}

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
	st.AlreadyInDB += st.Edges - st.EdgesWritten
	if err := touchWorks(im.catalog, relationEndpoints(edges)); err != nil {
		return st, err
	}
	return st, nil
}

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
			continue
		}
		m[sid] = r.Wid
	}
	return m, nil
}

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
