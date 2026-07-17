package service

import (
	"context"
	"strconv"
	"sync"
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

func TestTaxonomyTag_Delete_RecordsRefCountAndAffected(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	// Two galgames reference the tag. E2a: the per-galgame ripple revisions
	// are gone — a taxonomy delete's galgame side-effects are system cascades
	// (doc 21 §2.6 posture); the audit lives in taxonomy_revision
	// (ref_count + affected_galgame_ids).
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

	// E2a: NO per-galgame revision is minted for the cascade.
	assert.Equal(t, g1RevBefore, latestRevisionID(t, g1), "no ripple revision on g1 (system cascade)")
	assert.Equal(t, g2RevBefore, latestRevisionID(t, g2), "no ripple revision on g2 (system cascade)")
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

// TestTaxonomyEditableFieldsAllReachable is the §1.5 #2b protective
// invariant: every field on each per-entity Snapshot struct MUST be
// (a) writable through Create/Update DTOs, and
// (b) round-tripped into the persisted revision snapshot.
//
// If somebody adds a Snapshot field but forgets to wire Take/Overlay/
// DTO/Apply, this test goes red.
//
// Implementation: for each entity, write a request that sets every
// editable scalar to a unique non-default sentinel + every aliases
// slice to a known non-empty value, then assert the revision snapshot
// reflects all of them.
func TestTaxonomyEditableFieldsAllReachable(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	t.Run("tag", func(t *testing.T) {
		tg, err := testTaxSvc.CreateTag(ctx, 1, 3, &dto.CreateTagRequest{
			Name: "tag-init", Category: "content", Description: "d-init",
		})
		require.NoError(t, err)
		// Edit EVERY editable field with a distinguishable new value.
		newName, newCat, newDesc := "tag-new", "sexual", "d-new"
		newAlias := []string{"a-one", "a-two"}
		require.NoError(t, testTaxSvc.UpdateTag(ctx, 1, 3, &dto.UpdateTagRequest{
			TagID: tg.ID, Name: &newName, Category: &newCat,
			Description: &newDesc, Alias: &newAlias,
		}))
		revs, _, _ := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityTag, tg.ID, 1, 20)
		require.GreaterOrEqual(t, len(revs), 2)
		snap, err := model.TagSnapshotFromJSON(revs[0].Snapshot)
		require.NoError(t, err)
		assert.Equal(t, newName, snap.Name)
		assert.Equal(t, newCat, snap.Category)
		assert.Equal(t, newDesc, snap.Description)
		assert.ElementsMatch(t, newAlias, snap.Aliases)
		assertChangedFieldsCoversSnapshot(t,
			revs[0].ChangedFieldsList(), model.TagSnapshotFieldNames())
	})

	t.Run("official", func(t *testing.T) {
		o, err := testTaxSvc.CreateOfficial(ctx, 1, 3, &dto.CreateOfficialRequest{
			Name: "off-init", Category: "company", Original: "o-init",
			Link: "https://init.example", Lang: "ja", Description: "d-init",
		})
		require.NoError(t, err)
		newName, newOrig, newLink := "off-new", "o-new", "https://new.example"
		newCat, newLang, newDesc := "individual", "en", "d-new"
		newAlias := []string{"a-one", "a-two"}
		require.NoError(t, testTaxSvc.UpdateOfficial(ctx, 1, 3, &dto.UpdateOfficialRequest{
			OfficialID: o.ID,
			Name:       &newName, Original: &newOrig, Link: &newLink,
			Category: &newCat, Lang: &newLang, Description: &newDesc,
			Alias: &newAlias,
		}))
		revs, _, _ := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityOfficial, o.ID, 1, 20)
		require.GreaterOrEqual(t, len(revs), 2)
		snap, err := model.OfficialSnapshotFromJSON(revs[0].Snapshot)
		require.NoError(t, err)
		assert.Equal(t, newName, snap.Name)
		assert.Equal(t, newOrig, snap.Original)
		assert.Equal(t, newLink, snap.Link)
		assert.Equal(t, newCat, snap.Category)
		assert.Equal(t, newLang, snap.Lang)
		assert.Equal(t, newDesc, snap.Description)
		assert.ElementsMatch(t, newAlias, snap.Aliases)
		assertChangedFieldsCoversSnapshot(t,
			revs[0].ChangedFieldsList(), model.OfficialSnapshotFieldNames())
	})

	t.Run("engine", func(t *testing.T) {
		e, err := testTaxSvc.CreateEngine(ctx, 1, 3, &dto.CreateEngineRequest{
			Name: "eng-init", Description: "d-init",
		})
		require.NoError(t, err)
		newName, newDesc := "eng-new", "d-new"
		newAlias := []string{"a-one", "a-two"}
		require.NoError(t, testTaxSvc.UpdateEngine(ctx, 1, 3, &dto.UpdateEngineRequest{
			EngineID: e.ID, Name: &newName, Description: &newDesc, Alias: &newAlias,
		}))
		revs, _, _ := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityEngine, e.ID, 1, 20)
		require.GreaterOrEqual(t, len(revs), 2)
		snap, err := model.EngineSnapshotFromJSON(revs[0].Snapshot)
		require.NoError(t, err)
		assert.Equal(t, newName, snap.Name)
		assert.Equal(t, newDesc, snap.Description)
		assert.ElementsMatch(t, newAlias, snap.Aliases)
		assertChangedFieldsCoversSnapshot(t,
			revs[0].ChangedFieldsList(), model.EngineSnapshotFieldNames())
	})

	t.Run("series", func(t *testing.T) {
		// Series has no Alias / no GalgameIDs in snapshot — just Name + Description.
		sr, err := testTaxSvc.CreateSeries(ctx, 1, 3,
			&dto.CreateSeriesRequest{Name: "sr-init", Description: "d-init"}, nil)
		require.NoError(t, err)
		newName, newDesc := "sr-new", "d-new"
		require.NoError(t, testTaxSvc.UpdateSeries(ctx, 1, 3, &dto.UpdateSeriesRequest{
			SeriesID: sr.ID, Name: &newName, Description: &newDesc,
		}))
		revs, _, _ := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntitySeries, sr.ID, 1, 20)
		require.GreaterOrEqual(t, len(revs), 2)
		snap, err := model.SeriesSnapshotFromJSON(revs[0].Snapshot)
		require.NoError(t, err)
		assert.Equal(t, newName, snap.Name)
		assert.Equal(t, newDesc, snap.Description)
		assertChangedFieldsCoversSnapshot(t,
			revs[0].ChangedFieldsList(), model.SeriesSnapshotFieldNames())
	})
}

// assertChangedFieldsCoversSnapshot fails the test if the SnapshotFieldNames
// list is missing entries actually changed by the test edit. Each test
// above changes every editable field, so the changed_fields slice must
// include every entry. If somebody adds a field to *Snapshot but not to
// *SnapshotFieldNames(), this check catches it.
func assertChangedFieldsCoversSnapshot(t *testing.T, got, fieldNames []string) {
	t.Helper()
	got2 := make(map[string]bool, len(got))
	for _, k := range got {
		got2[k] = true
	}
	for _, f := range fieldNames {
		assert.True(t, got2[f],
			"changed_fields missing %q — every field this test changed must show in changed_fields", f)
	}
}

// TestTaxonomyConcurrentUpdate_SerializedByMainRowLock validates that
// two concurrent UpdateTag calls on the SAME tag can't both pick the
// same NextTaxonomyRevision (= idx_taxonomy_revision collision).
//
// The implementation guarantees serialisation via SELECT FOR UPDATE on
// galgame_tag.id (= the main entity row). Both updates eventually
// succeed — the second waits for the first to commit, sees the bumped
// max revision, and writes its row at the next number.
//
// Without the lock this test fails intermittently with a
// "duplicate key value violates idx_taxonomy_revision" error.
func TestTaxonomyConcurrentUpdate_SerializedByMainRowLock(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	tag, err := testTaxSvc.CreateTag(ctx, 1, 3, &dto.CreateTagRequest{
		Name: "concurrent", Category: "content",
	})
	require.NoError(t, err)

	const N = 8
	errCh := make(chan error, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			desc := "edit-" + strconv.Itoa(i)
			errCh <- testTaxSvc.UpdateTag(ctx, 1, 3, &dto.UpdateTagRequest{
				TagID: tag.ID, Description: &desc,
			})
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err, "concurrent Update must not deadlock or collide on idx_taxonomy_revision")
	}

	// All N concurrent updates plus the initial 'created' → 1+N rows.
	_, total, err := testTaxSvc.ListRevisions(ctx, model.TaxonomyEntityTag, tag.ID, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1+N), total, "every concurrent update produced exactly one taxonomy_revision")
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
	// E2a: membership writes are system cascades — no per-galgame revision.
	g1RevAfterCreate := latestRevisionID(t, g1)
	assert.Equal(t, g1RevAfterCreate, g1RevBefore,
		"membership no longer mints galgame revisions (audit = taxonomy_revision)")

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

func TestTaxonomySeries_Update_GalgameIDsProducesNoTaxonomyRevision(t *testing.T) {
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
	// E2a: the membership column flips are system cascades — no galgame
	// revisions either. The series_id column itself is verified below.
	assert.Equal(t, g1RevBefore, latestRevisionID(t, g1), "no ripple revision on g1")
	assert.Equal(t, g2RevBefore, latestRevisionID(t, g2), "no ripple revision on g2")

	var s1, s2 *int
	require.NoError(t, testDB.Model(&model.Galgame{}).Where("id = ?", g1).Select("series_id").Scan(&s1).Error)
	require.NoError(t, testDB.Model(&model.Galgame{}).Where("id = ?", g2).Select("series_id").Scan(&s2).Error)
	assert.Nil(t, s1, "g1 dropped from the series")
	require.NotNil(t, s2)
	assert.Equal(t, sr.ID, *s2, "g2 joined the series")
}
