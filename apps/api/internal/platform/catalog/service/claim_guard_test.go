package service

import (
	stderrors "errors"
	"testing"
	"time"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T8-⑤: ClaimWork — anchor-based claim of an existing unclaimed row,
// idempotency, conflict, and anchorless creation.
func TestClaimWork(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	// An unclaimed registry row with a bangumi exact anchor (an imported
	// subject whose product does not exist yet).
	unclaimed := createWork(t, "先に登録された作品")
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET status = ? WHERE id = ?`, model.WorkStatusStub, unclaimed.ID).Error)
	addExternalRef(t, model.EntityTypeWork, unclaimed.ID, 3, "subj-42", model.LinkKindExact)

	params := ClaimWorkParams{
		MediumID: 1, Site: "galgame_wiki", ProductWorkID: 4242,
		DisplayName: "先に登録された作品", OLang: "ja",
		Anchors: []ExternalAnchor{{SourceID: 3, ExternalID: "subj-42", MatchedBy: "import:test"}},
	}
	id, created, err := testWork.ClaimWork(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, unclaimed.ID, id, "claim must adopt the existing identity, never mint a second one")
	assert.False(t, created, "adopting an existing row is not a creation")

	var claimed model.CatalogWork
	require.NoError(t, testDB.First(&claimed, id).Error)
	require.NotNil(t, claimed.Site)
	assert.Equal(t, "galgame_wiki", *claimed.Site)
	require.NotNil(t, claimed.ProductWorkID)
	assert.Equal(t, int64(4242), *claimed.ProductWorkID)
	assert.Equal(t, model.WorkStatusLive, claimed.Status, "claiming graduates a stub to live")

	// Idempotency: the same product work returns the same id.
	again, againCreated, err := testWork.ClaimWork(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, id, again)
	assert.False(t, againCreated)

	// Conflict: a different product work claiming the same anchor.
	conflicting := params
	conflicting.Site = "letmoe"
	conflicting.ProductWorkID = 7
	_, _, err = testWork.ClaimWork(ctx, conflicting)
	require.ErrorIs(t, err, ErrClaimConflict)

	// Anchorless claim creates a new work with refs + created revision.
	fresh := ClaimWorkParams{
		MediumID: 1, Site: "galgame_wiki", ProductWorkID: 5000,
		DisplayName: "新規作品",
		Anchors:     []ExternalAnchor{{SourceID: 2, ExternalID: "v999", MatchedBy: "import:test"}},
	}
	freshID, freshCreated, err := testWork.ClaimWork(ctx, fresh)
	require.NoError(t, err)
	assert.NotEqual(t, id, freshID)
	assert.True(t, freshCreated, "anchorless claim mints a new registry row")
	var refCount, revCount int64
	testDB.Model(&model.CatalogExternalRef{}).
		Where("entity_type = ? AND entity_id = ? AND link_kind = ?", model.EntityTypeWork, freshID, model.LinkKindExact).
		Count(&refCount)
	assert.Equal(t, int64(1), refCount)
	testDB.Model(&model.CatalogRevision{}).
		Where("entity_type = ? AND entity_id = ? AND action = ?", model.EntityTypeWork, freshID, model.RevisionActionCreated).
		Count(&revCount)
	assert.Equal(t, int64(1), revCount)

	// Merging the claimed work into an unclaimed survivor TRANSFERS the
	// claim (survivorship claim unit) and moves the anchor; the same
	// product work re-claiming lands on the survivor, a different product
	// work conflicts.
	survivor := createWork(t, "存続側")
	pm, err := testMerge.ProposeMerge(ctx, model.EntityTypeWork, freshID, survivor.ID, 7, "dup")
	require.NoError(t, err)
	approveAndForceExecutable(t, pm.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, pm.ID, nil))

	var afterMerge model.CatalogWork
	require.NoError(t, testDB.First(&afterMerge, survivor.ID).Error)
	require.NotNil(t, afterMerge.Site)
	assert.Equal(t, int64(5000), *afterMerge.ProductWorkID, "the claim followed the merge onto the survivor")

	reclaimedID, _, err := testWork.ClaimWork(ctx, fresh) // same product work (5000)
	require.NoError(t, err)
	assert.Equal(t, survivor.ID, reclaimedID, "the claiming product resolves to the canonical work after the merge")

	other := fresh
	other.ProductWorkID = 5001
	_, _, err = testWork.ClaimWork(ctx, other)
	require.ErrorIs(t, err, ErrClaimConflict, "a different product work cannot steal the transferred claim")
}

// TestClaimWork_ReleaseAnchor covers the doujin claim path: a DLsite workno is
// a RELEASE-level anchor (R3/R5), so claiming by workno must (1) adopt the
// owning work through its release, inheriting its assets, (2) conflict with the
// structured owner when the work is already claimed, and (3) on a fresh mint
// hang the anchor on a new release — never the work.
func TestClaimWork_ReleaseAnchor(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	const dlsite int16 = 4

	// (1) ADOPT — an unclaimed rosetta-style work: a release carries the exact
	// DLsite anchor, plus a label asset that must survive the claim.
	unclaimed := createWork(t, "同人音声")
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET status = ? WHERE id = ?`, model.WorkStatusStub, unclaimed.ID).Error)
	rel := &model.CatalogRelease{WorkID: unclaimed.ID, Kind: model.ReleaseKindDigital}
	require.NoError(t, testDB.Create(rel).Error)
	addExternalRef(t, model.EntityTypeRelease, rel.ID, dlsite, "RJADOPT", model.LinkKindExact)
	label := &model.CatalogLabel{DisplayName: "既存サークル", Kind: model.LabelKindDoujinCircle, Lang: "ja"}
	require.NoError(t, testDB.Create(label).Error)
	require.NoError(t, testDB.Create(&model.CatalogWorkLabel{WorkID: unclaimed.ID, LabelID: label.ID, Kind: model.WorkLabelKindCircle}).Error)

	releaseAnchor := ExternalAnchor{SourceID: dlsite, ExternalID: "RJADOPT", MatchedBy: "import:test", EntityType: model.EntityTypeRelease}
	adoptParams := ClaimWorkParams{
		MediumID: 1, Site: "letmoe", ProductWorkID: 8001, DisplayName: "同人音声", OLang: "ja",
		Anchors: []ExternalAnchor{releaseAnchor},
	}
	id, created, err := testWork.ClaimWork(ctx, adoptParams)
	require.NoError(t, err)
	assert.Equal(t, unclaimed.ID, id, "the release anchor resolves to the owning work — no second identity")
	assert.False(t, created, "adopting an existing identity is not a mint")

	var claimed model.CatalogWork
	require.NoError(t, testDB.First(&claimed, id).Error)
	require.NotNil(t, claimed.Site)
	assert.Equal(t, "letmoe", *claimed.Site)
	assert.Equal(t, model.WorkStatusLive, claimed.Status, "claiming graduates the stub")
	var labelCount int64
	testDB.Model(&model.CatalogWorkLabel{}).Where("work_id = ?", id).Count(&labelCount)
	assert.Equal(t, int64(1), labelCount, "the work's existing assets are inherited by the claim")

	// Idempotency on the release-anchor path: same product work → same id.
	again, _, err := testWork.ClaimWork(ctx, adoptParams)
	require.NoError(t, err)
	assert.Equal(t, id, again)

	// (2) CONFLICT — a different product work claiming the same anchor gets the
	// structured owner (site + product work id), not just a sentinel.
	conflicting := adoptParams
	conflicting.ProductWorkID = 8002
	_, _, err = testWork.ClaimWork(ctx, conflicting)
	require.ErrorIs(t, err, ErrClaimConflict)
	var ce *ConflictError
	require.True(t, stderrors.As(err, &ce), "conflict must be a *ConflictError")
	assert.Equal(t, id, ce.WorkID)
	assert.Equal(t, "letmoe", ce.OwningSite)
	require.NotNil(t, ce.OwningProductWorkID)
	assert.Equal(t, int64(8001), *ce.OwningProductWorkID)

	// (3) MINT — a workno with no existing anchor mints a new work whose DLsite
	// anchor lands on a fresh RELEASE, not the work (collision-safe with a later
	// release-keyed import).
	mintParams := ClaimWorkParams{
		MediumID: 1, Site: "letmoe", ProductWorkID: 8003, DisplayName: "新規同人音声", OLang: "ja",
		Anchors: []ExternalAnchor{{SourceID: dlsite, ExternalID: "RJMINT", MatchedBy: "import:test", EntityType: model.EntityTypeRelease}},
	}
	mintID, mintCreated, err := testWork.ClaimWork(ctx, mintParams)
	require.NoError(t, err)
	assert.True(t, mintCreated, "an unmatched anchor mints a new work")

	var workLevelRefs int64
	testDB.Model(&model.CatalogExternalRef{}).
		Where("entity_type = ? AND source_id = ? AND external_id = ?", model.EntityTypeWork, dlsite, "RJMINT").
		Count(&workLevelRefs)
	assert.Equal(t, int64(0), workLevelRefs, "a SKU anchor must NOT be written at the work level")

	var mintRelID int64
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_release WHERE work_id = ?`, mintID).Scan(&mintRelID).Error)
	require.NotZero(t, mintRelID, "the mint created a release to carry the SKU anchor")
	var releaseLevelRefs int64
	testDB.Model(&model.CatalogExternalRef{}).
		Where("entity_type = ? AND entity_id = ? AND source_id = ? AND external_id = ? AND link_kind = ?",
			model.EntityTypeRelease, mintRelID, dlsite, "RJMINT", model.LinkKindExact).
		Count(&releaseLevelRefs)
	assert.Equal(t, int64(1), releaseLevelRefs, "the SKU anchor is an exact ref on the new release")

	// A work-natured anchor on a fresh mint stays on the work (no stray release).
	workAnchorParams := ClaimWorkParams{
		MediumID: 1, Site: "letmoe", ProductWorkID: 8004, DisplayName: "作品アンカー", OLang: "ja",
		Anchors: []ExternalAnchor{{SourceID: 3, ExternalID: "subj-77", MatchedBy: "import:test"}}, // no EntityType → work-level
	}
	waID, _, err := testWork.ClaimWork(ctx, workAnchorParams)
	require.NoError(t, err)
	var waReleases int64
	testDB.Model(&model.CatalogRelease{}).Where("work_id = ?", waID).Count(&waReleases)
	assert.Equal(t, int64(0), waReleases, "a work-level anchor adds no empty release")
	var waWorkRef int64
	testDB.Model(&model.CatalogExternalRef{}).
		Where("entity_type = ? AND entity_id = ? AND source_id = ? AND external_id = ?", model.EntityTypeWork, waID, 3, "subj-77").
		Count(&waWorkRef)
	assert.Equal(t, int64(1), waWorkRef, "the work-natured anchor stays on the work")
}

// T8-⑦: the usage delete-guard (doc 10 invariant 8).
func TestUsageGuard(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	used := createPerson(t, "referenced")
	free := createPerson(t, "unreferenced")
	addUsage(t, model.EntityTypePerson, used.ID, "kungal", time.Now())

	require.ErrorIs(t, testGuard.AssertDeletable(ctx, model.EntityTypePerson, used.ID), ErrHasUsage)
	require.NoError(t, testGuard.AssertDeletable(ctx, model.EntityTypePerson, free.ID))
}

// T8-⑧: the COALESCE expression unique on credits — NULL character_id
// collides, distinct characters coexist.
func TestCreditUniqueExpressionIndex(t *testing.T) {
	cleanTables(t)

	role := seededRoleID(t)
	w := createWork(t, "w")
	n := createCreditName(t, nil, "orphan name") // orphan credit names are legal
	createCredit(t, w.ID, n.ID, role, nil)

	dup := &model.CatalogCredit{WorkID: w.ID, CreditNameID: n.ID, RoleID: role, Spoiler: model.SpoilerNone}
	err := testDB.Create(dup).Error
	require.Error(t, err, "uncharactered duplicate must violate uq_catalog_credit")
	assert.Contains(t, err.Error(), "uq_catalog_credit")

	ch := &model.CatalogCharacter{DisplayName: "ヒロイン"}
	require.NoError(t, testDB.Create(ch).Error)
	withChar := &model.CatalogCredit{WorkID: w.ID, CreditNameID: n.ID, RoleID: role, CharacterID: &ch.ID, Spoiler: model.SpoilerNone}
	require.NoError(t, testDB.Create(withChar).Error, "distinct character makes the edge unique again")

	dupChar := &model.CatalogCredit{WorkID: w.ID, CreditNameID: n.ID, RoleID: role, CharacterID: &ch.ID, Spoiler: model.SpoilerNone}
	err = testDB.Create(dupChar).Error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uq_catalog_credit")
}
