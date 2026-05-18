package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression: a galgame edit must persist tag/official/engine relation
// changes through the snapshot/ApplySnapshot path, the recorded revision
// snapshot must equal the resulting DB state (canonical), and pointer
// presence semantics must hold (nil = keep, []  = clear). Pre-fix,
// PUT /galgame/:gid silently dropped all relation edits AND recorded a
// snapshot taken from the un-mutated relations (corrupt history).

func sp(s string) *string { return &s }

func tagIDsOf(t *testing.T, gid int) []int {
	t.Helper()
	var ids []int
	require.NoError(t, testDB.Model(&model.GalgameTagRelation{}).
		Where("galgame_id = ?", gid).Order("tag_id").Pluck("tag_id", &ids).Error)
	return ids
}

func latestRevSnapshot(t *testing.T, gid int) *model.Snapshot {
	t.Helper()
	var rev model.GalgameRevision
	require.NoError(t, testDB.Where("galgame_id = ?", gid).
		Order("revision DESC").First(&rev).Error)
	snap, err := model.SnapshotFromJSON(rev.Snapshot)
	require.NoError(t, err)
	return snap
}

func revCount(t *testing.T, gid int) int64 {
	t.Helper()
	var n int64
	testDB.Model(&model.GalgameRevision{}).Where("galgame_id = ?", gid).Count(&n)
	return n
}

func TestUpdate_RelationOverlaySemantics(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	gid := makeGalgame(t) // UserID=1, Status=0
	t1 := createTestTag(t, "t1", "content")
	t2 := createTestTag(t, "t2", "content")
	t3 := createTestTag(t, "t3", "content")
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: t1}).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: gid, TagID: t2}).Error)

	// (1) Omitting tag_ids (nil) must NOT touch relations — a name-only
	// edit can't silently wipe tags.
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		NameZhCN: sp("新名字"),
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{t1, t2}, tagIDsOf(t, gid), "nil tag_ids must keep relations")
	// Revision snapshot must reflect actual state (canonical, sorted).
	snap := latestRevSnapshot(t, gid)
	assert.Equal(t, []int{t1, t2}, snap.TagIDs)
	assert.Equal(t, "新名字", snap.NameZhCN)

	// (2) Explicit set = authoritative full replacement.
	newIDs := []int{t3, t2}
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		TagIDs: &newIDs,
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{t2, t3}, tagIDsOf(t, gid))
	assert.Equal(t, []int{t2, t3}, latestRevSnapshot(t, gid).TagIDs, "snapshot canonical-sorted == DB")

	// (3) Explicit empty slice clears all relations.
	empty := []int{}
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		TagIDs: &empty,
	})
	require.NoError(t, err)
	assert.Empty(t, tagIDsOf(t, gid))
	assert.Empty(t, latestRevSnapshot(t, gid).TagIDs)

	// (4) No-op edit (identical empty again, nothing else) produces NO
	// new revision.
	before := revCount(t, gid)
	_, err = testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		TagIDs: &empty,
	})
	require.NoError(t, err)
	assert.Equal(t, before, revCount(t, gid), "no-change edit must not create a revision")
}

func TestUpdate_OfficialAndEngineRelations(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	gid := makeGalgame(t)
	o1 := createTestOfficial(t, "o1", "company")
	e1 := createTestEngine(t, "e1")

	oIDs := []int{o1}
	eIDs := []int{e1}
	_, err := testSvc.Update(ctx, 1, gid, nil, &dto.UpdateGalgameRequest{
		OfficialIDs: &oIDs,
		EngineIDs:   &eIDs,
	})
	require.NoError(t, err)

	var oRel, eRel int64
	testDB.Model(&model.GalgameOfficialRelation{}).Where("galgame_id = ?", gid).Count(&oRel)
	testDB.Model(&model.GalgameEngineRelation{}).Where("galgame_id = ?", gid).Count(&eRel)
	assert.Equal(t, int64(1), oRel)
	assert.Equal(t, int64(1), eRel)

	snap := latestRevSnapshot(t, gid)
	assert.Equal(t, []int{o1}, snap.OfficialIDs)
	assert.Equal(t, []int{e1}, snap.EngineIDs)
}
