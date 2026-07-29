// merge_wikirefs_test.go — the wiki id-map exemption from the merge's
// anti-double-exact demotion (A2-0 §4c-1). Integration against
// kun_catalog_test (service_test.go TestMain).
package service

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wikiSourceID resolves the galgame_wiki registry source id.
func wikiSourceID(t *testing.T) int16 {
	t.Helper()
	var id int16
	if err := testDB.Raw(`SELECT id FROM catalog_source WHERE key = ?`, sourceKeyGalgameWiki).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("galgame_wiki source lookup: id=%d err=%v", id, err)
	}
	return id
}

// TestMergeKeepsWikiIdMapRefsExact pins both halves of the exemption in one
// merge: two labels each carrying their own wiki oid converge, and BOTH oids
// must stay exact on the survivor — a wiki id is an address-book entry, and N
// old ids legitimately point at one surviving entity (that is what a merge
// MEANS for the redirect promise). Meanwhile two competing vndb exacts on the
// same pair still demote, which is the doctrine the exemption carves out of,
// not one it replaces.
func TestMergeKeepsWikiIdMapRefsExact(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	wikiSrc := wikiSourceID(t)

	target := &model.CatalogLabel{DisplayName: "Survivor Brand", Kind: model.LabelKindGameBrand}
	require.NoError(t, testDB.Create(target).Error)
	source := &model.CatalogLabel{DisplayName: "Merged Brand", Kind: model.LabelKindGameBrand}
	require.NoError(t, testDB.Create(source).Error)

	// The wiki id map: one oid per side. Both must survive as exact.
	addExternalRef(t, model.EntityTypeLabel, target.ID, wikiSrc, "1001", model.LinkKindExact)
	addExternalRef(t, model.EntityTypeLabel, source.ID, wikiSrc, "1002", model.LinkKindExact)
	// An upstream source with the same double-exact shape: the regression the
	// exemption must NOT weaken — competing vndb ids are a real contradiction.
	addExternalRef(t, model.EntityTypeLabel, target.ID, srcVNDB, "p111", model.LinkKindExact)
	addExternalRef(t, model.EntityTypeLabel, source.ID, srcVNDB, "p222", model.LinkKindExact)

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeLabel, source.ID, target.ID, 7, "same brand")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	type refRow struct {
		SourceID   int16
		ExternalID string
		LinkKind   int16
	}
	var refs []refRow
	require.NoError(t, testDB.Raw(`SELECT source_id, external_id, link_kind FROM catalog_external_ref
		WHERE entity_type = ? AND entity_id = ? ORDER BY source_id, external_id`,
		model.EntityTypeLabel, target.ID).Scan(&refs).Error)

	assert.Equal(t, []refRow{
		{srcVNDB, "p111", model.LinkKindProbable},
		{srcVNDB, "p222", model.LinkKindProbable},
		{wikiSrc, "1001", model.LinkKindExact},
		{wikiSrc, "1002", model.LinkKindExact},
	}, refs, "both wiki oids must stay exact; the competing vndb exacts must still demote")
}

// TestMergeKeepsWikiGidRefsExactOnWorks is the same exemption on the WORK
// family — the gid map behind /galgame/<gid>, which is where the redirect
// promise is most load-bearing (the single biggest live URL space).
func TestMergeKeepsWikiGidRefsExactOnWorks(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	wikiSrc := wikiSourceID(t)

	target := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Survivor")
	source := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Duplicate")
	addExternalRef(t, model.EntityTypeWork, target.ID, wikiSrc, "5001", model.LinkKindExact)
	addExternalRef(t, model.EntityTypeWork, source.ID, wikiSrc, "5002", model.LinkKindExact)

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeWork, source.ID, target.ID, 7, "same game")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	var kinds []int16
	require.NoError(t, testDB.Raw(`SELECT link_kind FROM catalog_external_ref
		WHERE entity_type = ? AND entity_id = ? AND source_id = ? ORDER BY external_id`,
		model.EntityTypeWork, target.ID, wikiSrc).Scan(&kinds).Error)
	assert.Equal(t, []int16{model.LinkKindExact, model.LinkKindExact}, kinds,
		"both wiki gids must stay exact so both old /galgame/<gid> URLs keep resolving")
}
