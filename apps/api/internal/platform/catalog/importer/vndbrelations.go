package importer

import (
	"api/internal/platform/catalog/model"

	"gorm.io/gorm/clause"
)

const (
	relFandiscOf  int64 = 4
	relSameSeries int64 = 7
)

var vndbVNRelations = map[string]bgmRelSpec{
	"seq":  {typeID: relSequelOf, flip: true},
	"preq": {typeID: relSequelOf, flip: false},
	"side": {typeID: relSideStoryOf, flip: true},
	"par":  {typeID: relSideStoryOf, flip: false},
	"fan":  {typeID: relFandiscOf, flip: true},
	"orig": {typeID: relFandiscOf, flip: false},
	"set":  {typeID: relSameSetting, symmetric: true},
	"alt":  {typeID: relAlternativeVersion, symmetric: true},
	"char": {typeID: relSharesCharacter, symmetric: true},
	"ser":  {typeID: relSameSeries, symmetric: true},
}

type VNDBRelationStats struct {
	TotalRows         int
	Mapped            int
	Edges             int
	EdgesWritten      int
	AlreadyInDB       int
	InverseFolded     int
	SkippedUnmapped   int
	SkippedUnanchored int
	SkippedSelf       int
}

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
	if err := touchWorks(im.catalog, relationEndpoints(edges)); err != nil {
		return st, err
	}
	return st, nil
}
