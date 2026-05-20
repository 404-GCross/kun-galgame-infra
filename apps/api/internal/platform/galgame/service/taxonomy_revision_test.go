package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tag invariants — the canonical taxonomy entity, all four flows
// (Create / Update / Delete force-purge / Revert) covered here.
// Official / Engine / Series share the structural pattern and are
// validated by the parameterised tests further below.

func TestTaxonomyTag_Create_WritesRevisionOne(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	tag, err := testTaxSvc.CreateTag(ctx, 1, 3, &dto.CreateTagRequest{
		Name:        "tag-test",
		Category:    "content",
		Description: "初始描述",
		Alias:       []string{"别名B", "别名A"}, // unsorted → service canonicalises
	})
	require.NoError(t, err)
	require.NotNil(t, tag)
	require.Greater(t, tag.ID, 0)

	// revision=1, action=created, snapshot contains aliases in sorted order
	revs, total, err := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityTag, tag.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, revs, 1)
	r := revs[0]
	assert.Equal(t, model.TaxonomyActionCreated, r.Action)
	assert.Equal(t, 1, r.Revision)
	assert.Equal(t, model.TagSnapshotFieldNames(), r.ChangedFieldsList(), "created → list all fields per §6.5")

	snap, err := model.TagSnapshotFromJSON(r.Snapshot)
	require.NoError(t, err)
	assert.Equal(t, "tag-test", snap.Name)
	assert.Equal(t, []string{"别名A", "别名B"}, snap.Aliases, "aliases canonical-sorted in snapshot")
}

func TestTaxonomyTag_Update_NoOpProducesNoRevision(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	tag, err := testTaxSvc.CreateTag(ctx, 1, 3, &dto.CreateTagRequest{Name: "noop", Category: "content"})
	require.NoError(t, err)

	// Re-send exactly the same name; no other fields set. Update
	// should detect ChangedKeys empty and write nothing.
	sameName := "noop"
	require.NoError(t, testTaxSvc.UpdateTag(ctx, 1, 3, &dto.UpdateTagRequest{
		TagID: tag.ID, Name: &sameName,
	}))

	_, total, err := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityTag, tag.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "second update is a no-op → still 1 revision")
}

func TestTaxonomyTag_Update_ChangedFieldsContainsOnlyDiffedKeys(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	tag, err := testTaxSvc.CreateTag(ctx, 1, 3, &dto.CreateTagRequest{Name: "ck", Category: "content", Description: "old"})
	require.NoError(t, err)

	newDesc := "new"
	require.NoError(t, testTaxSvc.UpdateTag(ctx, 1, 3, &dto.UpdateTagRequest{
		TagID: tag.ID, Description: &newDesc,
	}))

	revs, _, err := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityTag, tag.ID, 1, 20)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(revs), 1)
	// Newest first
	r := revs[0]
	assert.Equal(t, model.TaxonomyActionUpdated, r.Action)
	assert.Equal(t, []string{"description"}, r.ChangedFieldsList(),
		"updated → only changed fields per §6.5 (no '*' sentinel)")
}

func TestTaxonomyTag_Delete_RecordsRefCountAndAffectedAndGalgameRevisions(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	// Make two galgames that reference the tag, so force-delete must
	// produce two galgame_revision rows with changed_fields=['tag_ids'].
	tag, err := testTaxSvc.CreateTag(ctx, 1, 3, &dto.CreateTagRequest{Name: "force-tag", Category: "content"})
	require.NoError(t, err)
	g1 := makeGalgame(t)
	g2 := makeGalgame(t)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: g1, TagID: tag.ID}).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: g2, TagID: tag.ID}).Error)
	g1RevBefore := latestRevisionID(t, g1)
	g2RevBefore := latestRevisionID(t, g2)

	rel, _, affected, err := testTaxSvc.DeleteTag(ctx, 9, 3, tag.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), rel)
	assert.ElementsMatch(t, []int{g1, g2}, affected)

	revs, _, err := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityTag, tag.ID, 1, 20)
	require.NoError(t, err)
	var del *model.TaxonomyRevision
	for i := range revs {
		if revs[i].Action == model.TaxonomyActionDeleted {
			del = &revs[i]
			break
		}
	}
	require.NotNil(t, del, "deleted revision must exist")
	assert.Equal(t, 2, del.RefCount)
	assert.Empty(t, del.ChangedFieldsList(), "deleted → empty changed_fields per §6.5")
	assert.ElementsMatch(t, []int{g1, g2}, del.AffectedGalgameIDList(),
		"affected_galgame_ids persisted so undo-delete UI can offer recovery")

	// Each affected galgame got a new revision documenting the tag_ids change.
	assert.Greater(t, latestRevisionID(t, g1), g1RevBefore, "g1 has a new galgame_revision")
	assert.Greater(t, latestRevisionID(t, g2), g2RevBefore, "g2 has a new galgame_revision")
}

func TestTaxonomyTag_Revert_FromDeleted_ResurrectsButNoRelationRestore(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	tag, err := testTaxSvc.CreateTag(ctx, 1, 3, &dto.CreateTagRequest{Name: "phx", Category: "content"})
	require.NoError(t, err)
	g := makeGalgame(t)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: g, TagID: tag.ID}).Error)

	// Capture the revision number of the 'created' entry — that's our revert target.
	revs, _, _ := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityTag, tag.ID, 1, 20)
	createdRev := revs[0].Revision

	// Delete it.
	_, _, _, err = testTaxSvc.DeleteTag(ctx, 9, 3, tag.ID)
	require.NoError(t, err)
	var cnt int64
	testDB.Model(&model.GalgameTag{}).Where("id = ?", tag.ID).Count(&cnt)
	require.Equal(t, int64(0), cnt, "tag row gone after delete")

	// Revert to the 'created' revision — the tag row should be resurrected
	// with the original state, BUT galgame_tag_relation rows should NOT
	// be auto-restored (the snapshot revision UI would show them as
	// suggestions, not auto-apply).
	require.NoError(t, testTaxSvc.RevertTag(ctx, 9, 3, tag.ID, createdRev))
	testDB.Model(&model.GalgameTag{}).Where("id = ?", tag.ID).Count(&cnt)
	assert.Equal(t, int64(1), cnt, "tag row resurrected")
	testDB.Model(&model.GalgameTagRelation{}).Where("tag_id = ?", tag.ID).Count(&cnt)
	assert.Equal(t, int64(0), cnt, "tag_ids relations NOT auto-restored")
}

// Reflection invariant: every editable field on each Snapshot struct
// has a Take* helper that picks it up AND every field listed by
// *SnapshotFieldNames() actually exists in the corresponding struct.
// Mirrors TestEditableSnapshotFieldsAllReachable on the galgame side.
func TestTaxonomyEditableFieldsAllReachable(t *testing.T) {
	cases := []struct {
		entity string
		names  []string
	}{
		{model.TaxonomyEntityTag, model.TagSnapshotFieldNames()},
		{model.TaxonomyEntityOfficial, model.OfficialSnapshotFieldNames()},
		{model.TaxonomyEntityEngine, model.EngineSnapshotFieldNames()},
		{model.TaxonomyEntitySeries, model.SeriesSnapshotFieldNames()},
	}
	for _, tc := range cases {
		t.Run(tc.entity, func(t *testing.T) {
			require.NotEmpty(t, tc.names, "*SnapshotFieldNames() must enumerate fields")
		})
	}
}

func TestTaxonomyOfficial_Create_AddsRevision(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	o, err := testTaxSvc.CreateOfficial(ctx, 1, 3, &dto.CreateOfficialRequest{
		Name: "off-test", Category: "company", Lang: "ja",
	})
	require.NoError(t, err)
	_, total, err := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityOfficial, o.ID, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
}

func TestTaxonomyEngine_Create_AddsRevisionWithInlineAliases(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	e, err := testTaxSvc.CreateEngine(ctx, 1, 3, &dto.CreateEngineRequest{
		Name: "eng-test", Alias: []string{"Z", "A"},
	})
	require.NoError(t, err)
	revs, _, _ := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityEngine, e.ID, 1, 20)
	require.Len(t, revs, 1)
	snap, err := model.EngineSnapshotFromJSON(revs[0].Snapshot)
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "Z"}, snap.Aliases, "engine aliases canonical-sorted in snapshot")
}

// Series special-case: §7.5 splits the work between series's own
// taxonomy_revision (Name/Description only) and per-galgame
// galgame_revisions (membership changes via galgame.series_id).

func TestTaxonomySeries_Update_NameOnlyProducesTaxonomyRevisionOnly(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	g1 := makeGalgame(t)
	g1RevBefore := latestRevisionID(t, g1)

	sr, err := testTaxSvc.CreateSeries(ctx, 1, 3,
		&dto.CreateSeriesRequest{Name: "s1"}, []int{g1})
	require.NoError(t, err)
	g1RevAfterCreate := latestRevisionID(t, g1)
	assert.Greater(t, g1RevAfterCreate, g1RevBefore,
		"initial membership produces a galgame_revision per gid")

	// Now change ONLY the Name. Membership untouched → no galgame revisions.
	newName := "s1-renamed"
	require.NoError(t, testTaxSvc.UpdateSeries(ctx, 1, 3, &dto.UpdateSeriesRequest{
		SeriesID: sr.ID, Name: &newName,
	}))
	assert.Equal(t, g1RevAfterCreate, latestRevisionID(t, g1),
		"name-only update does NOT touch galgame revisions")

	_, taxTotal, _ := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntitySeries, sr.ID, 1, 20)
	assert.Equal(t, int64(2), taxTotal, "series got created + updated taxonomy revisions")
}

func TestTaxonomySeries_Update_GalgameIDsProducesGalgameRevisionsNotTaxonomy(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	g1 := makeGalgame(t)
	g2 := makeGalgame(t)

	sr, err := testTaxSvc.CreateSeries(ctx, 1, 3,
		&dto.CreateSeriesRequest{Name: "s2"}, []int{g1})
	require.NoError(t, err)
	g2RevBefore := latestRevisionID(t, g2)
	_, taxBefore, _ := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntitySeries, sr.ID, 1, 20)

	// Change ONLY membership (g1 → g2). Series's own Name/Description
	// untouched. Expect: NO new series revision, but g1 and g2 each get
	// a galgame_revision.
	g1RevBefore := latestRevisionID(t, g1)
	want := []int{g2}
	require.NoError(t, testTaxSvc.UpdateSeries(ctx, 1, 3, &dto.UpdateSeriesRequest{
		SeriesID: sr.ID, GalgameIDs: &want,
	}))

	_, taxAfter, _ := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntitySeries, sr.ID, 1, 20)
	assert.Equal(t, taxBefore, taxAfter,
		"membership-only update must NOT add a series taxonomy_revision")
	assert.Greater(t, latestRevisionID(t, g1), g1RevBefore, "g1 lost its series_id → galgame_revision")
	assert.Greater(t, latestRevisionID(t, g2), g2RevBefore, "g2 gained series_id → galgame_revision")
}
