package service

import (
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
	id, err := testWork.ClaimWork(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, unclaimed.ID, id, "claim must adopt the existing identity, never mint a second one")

	var claimed model.CatalogWork
	require.NoError(t, testDB.First(&claimed, id).Error)
	require.NotNil(t, claimed.Site)
	assert.Equal(t, "galgame_wiki", *claimed.Site)
	require.NotNil(t, claimed.ProductWorkID)
	assert.Equal(t, int64(4242), *claimed.ProductWorkID)
	assert.Equal(t, model.WorkStatusLive, claimed.Status, "claiming graduates a stub to live")

	// Idempotency: the same product work returns the same id.
	again, err := testWork.ClaimWork(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, id, again)

	// Conflict: a different product work claiming the same anchor.
	conflicting := params
	conflicting.Site = "letmoe"
	conflicting.ProductWorkID = 7
	_, err = testWork.ClaimWork(ctx, conflicting)
	require.ErrorIs(t, err, ErrClaimConflict)

	// Anchorless claim creates a new work with refs + created revision.
	fresh := ClaimWorkParams{
		MediumID: 1, Site: "galgame_wiki", ProductWorkID: 5000,
		DisplayName: "新規作品",
		Anchors:     []ExternalAnchor{{SourceID: 2, ExternalID: "v999", MatchedBy: "import:test"}},
	}
	freshID, err := testWork.ClaimWork(ctx, fresh)
	require.NoError(t, err)
	assert.NotEqual(t, id, freshID)
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

	reclaimedID, err := testWork.ClaimWork(ctx, fresh) // same product work (5000)
	require.NoError(t, err)
	assert.Equal(t, survivor.ID, reclaimedID, "the claiming product resolves to the canonical work after the merge")

	other := fresh
	other.ProductWorkID = 5001
	_, err = testWork.ClaimWork(ctx, other)
	require.ErrorIs(t, err, ErrClaimConflict, "a different product work cannot steal the transferred claim")
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
