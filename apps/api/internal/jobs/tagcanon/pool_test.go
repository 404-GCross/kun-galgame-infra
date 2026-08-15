package tagcanon

import (
	"context"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProposePoolDropsEntityNamesAndRejections(t *testing.T) {
	cleanTagcanon(t)
	ctx := context.Background()
	bgm := srcID(t, sourceKeyBangumi)
	var medium int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key='galgame'`).Scan(&medium).Error)

	label := model.CatalogLabel{DisplayName: "オトメイト", Kind: model.LabelKindGameBrand}
	require.NoError(t, testDB.Create(&label).Error)
	credit := model.CatalogCreditName{Name: "御苑生メイ"}
	require.NoError(t, testDB.Create(&credit).Error)
	elf := model.CatalogLabel{DisplayName: "ELF", Kind: model.LabelKindGameBrand}
	require.NoError(t, testDB.Create(&elf).Error)
	t.Cleanup(func() {
		testDB.Unscoped().Delete(&model.CatalogLabel{}, []int64{label.ID, elf.ID})
		testDB.Delete(&model.CatalogCreditName{}, credit.ID)
	})

	// ELF is a brand AND a tag the catalog already answers with: the blocklist
	// must lose to the vocabulary, or the merge wave retires a live tag.
	require.NoError(t, testDB.Create(&model.CatalogTag{
		Name: "ELF", Tier: model.TagTierLongtail, Kind: model.TagKindContent,
	}).Error)
	require.NoError(t, testDB.Create(&model.CatalogTagRejection{
		SourceID: bgm, SourceName: "破鞋", Reason: "crude slang", RejectedBy: "wave-208",
	}).Error)
	mapped := mkTag(t, "百合", false)
	mkMap(t, bgm, "百合", mapped.ID)

	w := mkBodylessWork(t, medium)
	for _, name := range []string{"御苑生メイ", "オトメイト", "ELF", "破鞋", "百合", "银发"} {
		mkWorkTag(t, w, name, 3, bgm)
	}

	pool, err := buildPool(ctx, testDB, ProposeOpts{DSN: testDSN, SkipOriginals: true})
	require.NoError(t, err)
	got := make([]string, 0, len(pool))
	for _, c := range pool {
		got = append(got, c.Name)
	}
	assert.ElementsMatch(t, []string{"ELF", "银发"}, got)

	st, err := Backlog(ctx, BacklogOpts{DSN: testDSN, Threshold: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, st.Total)
	assert.Equal(t, 2, st.AboveThreshold)
	assert.Equal(t, 2, st.BySource[sourceKeyBangumi])
}
