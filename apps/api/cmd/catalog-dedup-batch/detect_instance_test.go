package main

import (
	"testing"
	"time"

	"api/internal/platform/catalog/model"
	srcv "api/internal/platform/catalog/srcvndb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectVndbInstanceDetox(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	require.NoError(t, srcv.EnsureSchema(testDB))
	for _, tbl := range []string{
		"catalog_external_ref", "catalog_work_character", "catalog_credit",
		"catalog_character", "catalog_work", "src_vndb.chars",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	var srcVndb, srcBgm int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key='vndb'`).Scan(&srcVndb).Error)
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key='bangumi'`).Scan(&srcBgm).Error)

	mkWork := func(name string) int64 {
		w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: name}
		require.NoError(t, testDB.Create(&w).Error)
		return w.ID
	}
	mkChar := func(work int64, name string) int64 {
		c := model.CatalogCharacter{DisplayName: name, Lang: "ja"}
		require.NoError(t, testDB.Create(&c).Error)
		require.NoError(t, testDB.Create(&model.CatalogWorkCharacter{WorkID: work, CharacterID: c.ID, Kind: 1}).Error)
		return c.ID
	}
	mkAnchor := func(char int64, src int16, ext string) {
		require.NoError(t, testDB.Create(&model.CatalogExternalRef{
			EntityType: model.EntityTypeCharacter, EntityID: char, SourceID: src,
			ExternalID: ext, LinkKind: model.LinkKindExact, MatchedBy: "rule:test",
		}).Error)
	}
	mkVndbChar := func(id, main string) {
		require.NoError(t, testDB.Create(&srcv.Char{ID: id, Main: main, IngestedAt: time.Now()}).Error)
	}

	w1 := mkWork("いろとりどりのセカイ")
	cBase := mkChar(w1, "如月 澪")
	cInst := mkChar(w1, "如月 澪")
	cBgm := mkChar(w1, "如月澪")
	mkVndbChar("c1", "")
	mkVndbChar("c2", "c1")
	mkAnchor(cBase, srcVndb, "c1")
	mkAnchor(cInst, srcVndb, "c2")
	mkAnchor(cBgm, srcBgm, "12236")

	w2 := mkWork("対照作品")
	cA := mkChar(w2, "同名 娘")
	cB := mkChar(w2, "同名娘")
	mkVndbChar("c3", "")
	mkVndbChar("c4", "")
	mkAnchor(cA, srcVndb, "c3")
	mkAnchor(cB, srcVndb, "c4")

	groups, st, err := detectCharacters(testDB)
	require.NoError(t, err)
	assert.Equal(t, 1, st.charInstanceDetox, "w1 bucket detoxed")
	assert.Equal(t, 1, st.charDirtyBkt, "w2 vndb double (no main link) stays dirty")
	require.Len(t, groups, 1)
	got := append([]int64{groups[0].survivor}, groups[0].sources...)
	assert.ElementsMatch(t, []int64{cBase, cBgm}, got, "bgm merges onto the vndb base; the instance is untouched")
}
