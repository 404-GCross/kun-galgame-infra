package service

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTrait is local to this file: the vndb trait vocabulary has no other
// test user yet, so it does not belong in the shared helper set.
func createTrait(t *testing.T, tid, name string) *model.CatalogCharacterTrait {
	t.Helper()
	tr := &model.CatalogCharacterTrait{VndbTID: tid, Name: name, GroupTID: "g"}
	require.NoError(t, testDB.Create(tr).Error)
	return tr
}

// TestMergeCharacterCarriesIntrosAndTraits pins the wave-154 fix: character
// intros (step 107) and vndb trait links (step 93) joined the character face
// after the merge hook list was written and were never re-hung, so a merge
// stranded them on the retired row where nothing reads them (770 such intro
// rows in the 2026-07-30 production snapshot). They must now follow the
// character onto its host, deduped against the tables' own unique keys.
func TestMergeCharacterCarriesIntrosAndTraits(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	target := createCharacter(t, "夏侯淵 妙才")
	source := createCharacter(t, "夏侯淵妙才")

	// Intros key on (character, lang, source): the ja/src2 pair collides and
	// the survivor's text must stand; ja/src3 and zh-Hans/src2 are new.
	mkIntro := func(cid int64, lang string, src int16, body string) {
		require.NoError(t, testDB.Create(&model.CatalogCharacterIntro{
			CharacterID: cid, Lang: lang, SourceID: src, Intro: body}).Error)
	}
	mkIntro(target.ID, "ja", 2, "target ja/2")
	mkIntro(source.ID, "ja", 2, "source ja/2 — loses to the survivor")
	mkIntro(source.ID, "ja", 3, "source ja/3 — moves")
	mkIntro(source.ID, "zh-Hans", 2, "source zh/2 — moves")

	// Trait links key on (character, trait): one collision, one new.
	shared := createTrait(t, "g1", "Shared")
	only := createTrait(t, "g2", "SourceOnly")
	mkLink := func(cid, tid int64, spoiler int16) {
		require.NoError(t, testDB.Create(&model.CatalogCharacterTraitLink{
			CharacterID: cid, TraitID: tid, SpoilerLevel: spoiler}).Error)
	}
	mkLink(target.ID, shared.ID, 0)
	mkLink(source.ID, shared.ID, 2) // collides → survivor's row stands
	mkLink(source.ID, only.ID, 1)   // moves

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeCharacter, source.ID, target.ID, 7, "wave 154")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	var intros []model.CatalogCharacterIntro
	require.NoError(t, testDB.Where("character_id = ?", target.ID).
		Order("lang, source_id").Find(&intros).Error)
	require.Len(t, intros, 3, "the two non-colliding source intros move onto the host")
	assert.Equal(t, "target ja/2", intros[0].Intro, "the colliding key keeps the survivor's text")
	assert.EqualValues(t, 3, intros[1].SourceID)
	assert.Equal(t, "zh-Hans", intros[2].Lang)

	var links []model.CatalogCharacterTraitLink
	require.NoError(t, testDB.Where("character_id = ?", target.ID).
		Order("trait_id").Find(&links).Error)
	require.Len(t, links, 2, "the non-colliding source trait link moves onto the host")
	assert.EqualValues(t, 0, links[0].SpoilerLevel, "the colliding key keeps the survivor's row")
	assert.Equal(t, only.ID, links[1].TraitID)

	// Nothing may be left stranded on the retired character.
	var strandedIntros, strandedLinks int64
	require.NoError(t, testDB.Model(&model.CatalogCharacterIntro{}).
		Where("character_id = ?", source.ID).Count(&strandedIntros).Error)
	require.NoError(t, testDB.Model(&model.CatalogCharacterTraitLink{}).
		Where("character_id = ?", source.ID).Count(&strandedLinks).Error)
	assert.Zero(t, strandedIntros)
	assert.Zero(t, strandedLinks)
}
