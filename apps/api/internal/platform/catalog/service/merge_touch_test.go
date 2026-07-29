package service

import (
	"testing"
	"time"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Wave-118 touch matrix: a merge rewrites the read face of works that are not
// themselves being merged (credits[], the roster, brand labels, relations[]),
// and those works' own rows are never written — so /v1/catalog/changes, an
// (updated_at, id) keyset over catalog_work, would stay silent about the whole
// merge. These tests pin which works the merge must touch and, just as
// importantly, which it must leave alone.

// settleWorks backdates every work an hour: a merge's now() stamp then reads
// as an unmistakable advance, and untouched rows sit well behind the changes
// feed's freshness lag.
func settleWorks(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET updated_at = now() - interval '1 hour'`).Error)
}

func workUpdatedAt(t *testing.T, id int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, testDB.Raw(`SELECT updated_at FROM catalog_work WHERE id = ?`, id).Scan(&ts).Error)
	return ts
}

// drainChanges walks the changes feed to its end and returns the cursor a
// caught-up consumer would hold.
func drainChanges(t *testing.T) string {
	t.Helper()
	page, err := newPublicSvc().Changes(t.Context(), "", 500)
	require.NoError(t, err)
	return page.NextCursor
}

// changesSince returns the work ids the feed serves after cursor. The merge
// stamps now(), which the feed's 5s freshness lag holds back on purpose (a
// separate mechanism, pinned by TestChangesFeedWatermarkLag), so every row is
// aged past the lag first — these tests are about the touch, not the lag.
func changesSince(t *testing.T, cursor string) []int64 {
	t.Helper()
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET updated_at = updated_at - interval '10 seconds'`).Error)
	page, err := newPublicSvc().Changes(t.Context(), cursor, 500)
	require.NoError(t, err)
	ids := make([]int64, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

// A credit_name merge rewrites credits[] on works that take no part in the
// merge: the repointed credit changes which name a work credits, and the dedup
// delete removes a row from the rendered set. Both hosts must move; a work
// without a credit from either side must not.
func TestMergeCreditNameTouchesHostWorks(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	person := createPerson(t, "one person")
	target := createCreditName(t, &person.ID, "表記")
	source := createCreditName(t, &person.ID, "表記(旧)")

	role := seededRoleID(t)
	repointHost := createWork(t, "repoint-host")
	dedupHost := createWork(t, "dedup-host")
	bystander := createWork(t, "bystander")
	createCredit(t, repointHost.ID, source.ID, role, nil) // moves to the target
	createCredit(t, dedupHost.ID, target.ID, role, nil)
	createCredit(t, dedupHost.ID, source.ID, role, nil) // duplicate → dropped

	settleWorks(t)
	before := map[int64]time.Time{
		repointHost.ID: workUpdatedAt(t, repointHost.ID),
		dedupHost.ID:   workUpdatedAt(t, dedupHost.ID),
		bystander.ID:   workUpdatedAt(t, bystander.ID),
	}

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeCreditName, source.ID, target.ID, 7, "same spelling")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	assert.True(t, workUpdatedAt(t, repointHost.ID).After(before[repointHost.ID]),
		"the work whose credit was repointed renders a different name now")
	assert.True(t, workUpdatedAt(t, dedupHost.ID).After(before[dedupHost.ID]),
		"the work that lost a duplicate credit renders one row fewer now")
	assert.True(t, workUpdatedAt(t, bystander.ID).Equal(before[bystander.ID]),
		"a work with no credit from either side must stay out of the feed")
}

// A label merge rewrites both faces a label reaches: the brand edges a work
// renders and the label a credit carries.
func TestMergeLabelTouchesHostWorks(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	target := &model.CatalogLabel{DisplayName: "Brand", Kind: model.LabelKindGameBrand}
	require.NoError(t, testDB.Create(target).Error)
	source := &model.CatalogLabel{DisplayName: "brand", Kind: model.LabelKindGameBrand}
	require.NoError(t, testDB.Create(source).Error)

	edgeHost := createWork(t, "edge-host")   // source-only brand edge moves
	dedupHost := createWork(t, "dedup-host") // duplicate brand edge dropped
	creditHost := createWork(t, "credit-host")
	bystander := createWork(t, "bystander")
	for _, e := range []model.CatalogWorkLabel{
		{WorkID: edgeHost.ID, LabelID: source.ID, Kind: model.WorkLabelKindCircle},
		{WorkID: dedupHost.ID, LabelID: target.ID, Kind: model.WorkLabelKindCircle},
		{WorkID: dedupHost.ID, LabelID: source.ID, Kind: model.WorkLabelKindCircle},
	} {
		require.NoError(t, testDB.Create(&e).Error)
	}
	person := createPerson(t, "staff")
	name := createCreditName(t, &person.ID, "名義")
	credit := createCredit(t, creditHost.ID, name.ID, seededRoleID(t), nil)
	require.NoError(t, testDB.Exec(`UPDATE catalog_credit SET label_id = ? WHERE id = ?`, source.ID, credit.ID).Error)

	settleWorks(t)
	before := map[int64]time.Time{
		edgeHost.ID:   workUpdatedAt(t, edgeHost.ID),
		dedupHost.ID:  workUpdatedAt(t, dedupHost.ID),
		creditHost.ID: workUpdatedAt(t, creditHost.ID),
		bystander.ID:  workUpdatedAt(t, bystander.ID),
	}

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeLabel, source.ID, target.ID, 7, "same brand")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	for _, id := range []int64{edgeHost.ID, dedupHost.ID, creditHost.ID} {
		assert.True(t, workUpdatedAt(t, id).After(before[id]), "work %d renders the merged label now", id)
	}
	assert.True(t, workUpdatedAt(t, bystander.ID).Equal(before[bystander.ID]),
		"a work touching neither label must stay out of the feed")
}

// A character merge rewrites the roster a work renders and the voice credits
// that point at the character — the highest-volume merge family (the step-98
// dedup batch), so its host works must reach the feed.
func TestMergeCharacterTouchesHostWorks(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	target := createCharacter(t, "冬月十夜")
	source := createCharacter(t, "冬月 十夜")

	rosterHost := createWork(t, "roster-host") // source-only edge moves
	dedupHost := createWork(t, "dedup-host")   // both sides → loser edge folds and drops
	creditHost := createWork(t, "credit-host") // voice credit repoints
	bystander := createWork(t, "bystander")
	createWorkCharacter(t, rosterHost.ID, source.ID, model.WorkCharacterKindMain, model.SpoilerNone)
	createWorkCharacter(t, dedupHost.ID, target.ID, model.WorkCharacterKindUnknown, model.SpoilerNone)
	createWorkCharacter(t, dedupHost.ID, source.ID, model.WorkCharacterKindSecondary, model.SpoilerSevere)
	person := createPerson(t, "VA")
	name := createCreditName(t, &person.ID, "声優")
	createCredit(t, creditHost.ID, name.ID, seededRoleID(t), &source.ID)

	settleWorks(t)
	before := map[int64]time.Time{}
	for _, id := range []int64{rosterHost.ID, dedupHost.ID, creditHost.ID, bystander.ID} {
		before[id] = workUpdatedAt(t, id)
	}

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeCharacter, source.ID, target.ID, 7, "same character")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	for _, id := range []int64{rosterHost.ID, dedupHost.ID, creditHost.ID} {
		assert.True(t, workUpdatedAt(t, id).After(before[id]), "work %d renders the merged character now", id)
	}
	assert.True(t, workUpdatedAt(t, bystander.ID).Equal(before[bystander.ID]),
		"a work with neither character must stay out of the feed")
}

// A work merge moves every facet onto the target AND rewrites relations[] on
// the works at the other end of the source's edges — those neighbours are not
// part of the merge and would otherwise never enter the feed. The source is
// deliberately not touched: it leaves the feed's predicate for good (status
// merged + soft delete) and its merge signal belongs to the redirects face.
func TestMergeWorkTouchesTargetAndRelationOtherEnd(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	dst := createWork(t, "target")
	src := createWork(t, "source")
	aSide := createWork(t, "a-side-neighbour") // edge source→neighbour repoints
	bSide := createWork(t, "b-side-neighbour") // edge neighbour→source repoints
	dupSide := createWork(t, "dup-neighbour")  // edge exists on both → source's drops
	bystander := createWork(t, "bystander")    // no edge at all
	createWorkRelation(t, src.ID, dst.ID)      // pair edge → dropped (a<>b CHECK)
	createWorkRelation(t, src.ID, aSide.ID)    // a-side repoint
	createWorkRelation(t, bSide.ID, src.ID)    // b-side repoint
	createWorkRelation(t, src.ID, dupSide.ID)  // dedup delete
	createWorkRelation(t, dst.ID, dupSide.ID)  // the duplicate it hits
	// A facet that simply moves onto the target (no third work involved).
	require.NoError(t, testDB.Create(&model.CatalogWorkTitle{
		WorkID: src.ID, Lang: "ja", Title: "移る題名", Kind: model.WorkTitleKindOfficial}).Error)

	settleWorks(t)
	before := map[int64]time.Time{}
	for _, id := range []int64{dst.ID, aSide.ID, bSide.ID, dupSide.ID, bystander.ID} {
		before[id] = workUpdatedAt(t, id)
	}
	cursor := drainChanges(t)

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeWork, src.ID, dst.ID, 7, "same work")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	assert.True(t, workUpdatedAt(t, dst.ID).After(before[dst.ID]),
		"the merge target always gains the source's facets")
	for _, id := range []int64{aSide.ID, bSide.ID, dupSide.ID} {
		assert.True(t, workUpdatedAt(t, id).After(before[id]),
			"work %d is at the other end of a rewritten relation", id)
	}
	assert.True(t, workUpdatedAt(t, bystander.ID).Equal(before[bystander.ID]),
		"a work with no edge to either side must stay out of the feed")

	ids := changesSince(t, cursor)
	assert.ElementsMatch(t, []int64{dst.ID, aSide.ID, bSide.ID, dupSide.ID}, ids,
		"the feed carries the target and the rewritten neighbours, nothing else")
	assert.NotContains(t, ids, src.ID, "a merged-away source must never enter the changes feed")
}

// The identity layer below the work face — person on a name, org on a label —
// never reaches what a work renders (a work lists credit names and label ids),
// so merging there must move no work into the feed at all.
func TestMergeIdentityLayerTouchesNoWork(t *testing.T) {
	t.Run("person", func(t *testing.T) {
		cleanTables(t)
		ctx := t.Context()

		target := createPerson(t, "target")
		source := createPerson(t, "source")
		name := createCreditName(t, &source.ID, "旧名義")
		w := createWork(t, "credited")
		createCredit(t, w.ID, name.ID, seededRoleID(t), nil)

		settleWorks(t)
		before := workUpdatedAt(t, w.ID)

		p, err := testMerge.ProposeMerge(ctx, model.EntityTypePerson, source.ID, target.ID, 7, "same person")
		require.NoError(t, err)
		approveAndForceExecutable(t, p.ID)
		require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

		var moved model.CatalogCreditName
		require.NoError(t, testDB.First(&moved, name.ID).Error)
		require.NotNil(t, moved.PersonID)
		require.Equal(t, target.ID, *moved.PersonID, "the merge must actually have rehung the name")
		assert.True(t, workUpdatedAt(t, w.ID).Equal(before),
			"person_id lives on the name face — the work renders the same bytes")
	})

	t.Run("org", func(t *testing.T) {
		cleanTables(t)
		ctx := t.Context()

		target := &model.CatalogOrg{DisplayName: "Target Org"}
		require.NoError(t, testDB.Create(target).Error)
		source := &model.CatalogOrg{DisplayName: "Source Org"}
		require.NoError(t, testDB.Create(source).Error)
		label := &model.CatalogLabel{DisplayName: "Brand", Kind: model.LabelKindGameBrand, OrgID: &source.ID}
		require.NoError(t, testDB.Create(label).Error)
		w := createWork(t, "branded")
		require.NoError(t, testDB.Create(&model.CatalogWorkLabel{
			WorkID: w.ID, LabelID: label.ID, Kind: model.WorkLabelKindCircle}).Error)

		settleWorks(t)
		before := workUpdatedAt(t, w.ID)

		p, err := testMerge.ProposeMerge(ctx, model.EntityTypeOrg, source.ID, target.ID, 7, "same org")
		require.NoError(t, err)
		approveAndForceExecutable(t, p.ID)
		require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

		var moved model.CatalogLabel
		require.NoError(t, testDB.First(&moved, label.ID).Error)
		require.NotNil(t, moved.OrgID)
		require.Equal(t, target.ID, *moved.OrgID, "the merge must actually have rehung the label")
		assert.True(t, workUpdatedAt(t, w.ID).Equal(before),
			"a work renders label ids, never the org behind them")
	})
}

// Claim transfer is the one survivorship step that writes catalog_work with
// raw SQL: the target adopts the source's claim, so its read face changes and
// the feed must say so — while the source, whose claim was cleared by the very
// same step, still stays out of the feed.
func TestMergeWorkClaimTransferTouchesTarget(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()

	dst := createWork(t, "unclaimed target")
	src := createWork(t, "claimed source")
	claimWork(t, src.ID, "kungal", 4242)

	settleWorks(t)
	before := workUpdatedAt(t, dst.ID)
	cursor := drainChanges(t)

	p, err := testMerge.ProposeMerge(ctx, model.EntityTypeWork, src.ID, dst.ID, 7, "claim moves")
	require.NoError(t, err)
	approveAndForceExecutable(t, p.ID)
	require.NoError(t, testMerge.ExecuteMerge(ctx, p.ID, nil))

	var merged model.CatalogWork
	require.NoError(t, testDB.First(&merged, dst.ID).Error)
	require.NotNil(t, merged.Site)
	assert.Equal(t, "kungal", *merged.Site)
	require.NotNil(t, merged.ProductWorkID)
	assert.Equal(t, int64(4242), *merged.ProductWorkID)
	assert.True(t, workUpdatedAt(t, dst.ID).After(before), "the target now carries the claim")

	ids := changesSince(t, cursor)
	assert.Equal(t, []int64{dst.ID}, ids, "only the claim's new owner enters the feed")
}
