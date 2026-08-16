package service

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	bangumiIntroSource = 3
	curatedIntroSource = 12
)

// catalog.label.intros is a registered curated lane, and its public face folds
// one row per language exactly like the work and character faces did — the same
// defect, found by the completeness sweep rather than by anyone hitting it.
func TestLabelIntroHumanLaneWinsTheLangFold(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	svc := newPublicSvc()

	label := createLabel(t, "ブランド", model.LabelKindGameBrand)
	require.NoError(t, testDB.Create(&[]model.CatalogLabelIntro{
		{LabelID: label, Lang: "ja", Intro: "上流の紹介", SourceID: bangumiIntroSource, Provenance: model.IntroProvenanceSource},
		{LabelID: label, Lang: "ja", Intro: "人手の紹介", SourceID: curatedIntroSource, Provenance: model.IntroProvenanceSource},
	}).Error)

	rec, ok, err := svc.Label(ctx, label, false, true, 10, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, rec.Intros, 1)
	assert.Equal(t, "人手の紹介", rec.Intros[0].Intro)
	assert.Equal(t, "curated", rec.Intros[0].Source)
}

// And no further: catalog_label_intro carries the 0/1 axis, so the machine tier
// must stay the importers' — the human lane decides only inside provenance=0.
func TestLabelIntroCuratedMachineRowDoesNotBeatUpstream(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	svc := newPublicSvc()

	label := createLabel(t, "ブランド", model.LabelKindGameBrand)
	require.NoError(t, testDB.Create(&[]model.CatalogLabelIntro{
		{LabelID: label, Lang: "zh-Hans", Intro: "上流の原文", SourceID: bangumiIntroSource, Provenance: model.IntroProvenanceSource},
		{LabelID: label, Lang: "zh-Hans", Intro: "curated 車道の機械翻訳", SourceID: curatedIntroSource, Provenance: model.IntroProvenanceMachine},
	}).Error)

	rec, ok, err := svc.Label(ctx, label, false, true, 10, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, rec.Intros, 1)
	assert.Equal(t, "上流の原文", rec.Intros[0].Intro)
	assert.False(t, rec.Intros[0].Machine)

	// Two machine rows: the human lane still decides nothing.
	require.NoError(t, testDB.Exec(
		`UPDATE catalog_label_intro SET provenance = ? WHERE label_id = ? AND source_id = ?`,
		model.IntroProvenanceMachine, label, bangumiIntroSource).Error)
	rec, ok, err = svc.Label(ctx, label, false, true, 10, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, rec.Intros, 1)
	assert.Equal(t, "上流の原文", rec.Intros[0].Intro)
}

// catalog_tag_intro has NO provenance column, so this face has no machine tier
// to protect and only the positive direction can be pinned. The ordering term is
// HumanLaneFirstNoProvenanceSQL for exactly that reason.
func TestTagIntroHumanLaneWinsTheLangFold(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	ctx := t.Context()
	svc := newPublicSvc()

	tag := model.CatalogTag{Name: "純愛", Tier: model.TagTierCore, Kind: model.TagKindContent}
	require.NoError(t, testDB.Create(&tag).Error)
	require.NoError(t, testDB.Create(&[]model.CatalogTagIntro{
		{TagID: tag.ID, Lang: "zh-Hans", Intro: "上游的说明", SourceID: bangumiIntroSource},
		{TagID: tag.ID, Lang: "zh-Hans", Intro: "人工的说明", SourceID: curatedIntroSource},
	}).Error)

	rec, ok, err := svc.TagDetail(ctx, tag.ID, false, true, 10, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, rec.Intros, 1)
	assert.Equal(t, "人工的说明", rec.Intros[0].Intro)
	assert.Equal(t, "curated", rec.Intros[0].Source)
}
