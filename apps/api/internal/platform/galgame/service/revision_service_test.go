package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper: create a galgame and return it
func createTestGalgame(t *testing.T, vndbID, name string) *model.Galgame {
	t.Helper()
	g, err := testSvc.Create(context.Background(), 1, &dto.CreateGalgameRequest{
		VNDBID:   vndbID,
		NameZhCN: name,
	})
	require.NoError(t, err)
	return g
}

func TestRevert(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v10001", "版本1的名字")

	// Update to version 2
	name2 := "版本2的名字"
	testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &name2})

	// Revert to version 1
	err := testSvc.Revert(ctx, 1, g.ID, 1, []string{"admin"})
	require.NoError(t, err)

	// Check the galgame is back to version 1's state
	var galgame model.Galgame
	testDB.First(&galgame, g.ID)
	assert.Equal(t, "版本1的名字", galgame.NameZhCN)

	// Should have 3 revisions: created, updated, reverted
	var count int64
	testDB.Model(&model.GalgameRevision{}).Where("galgame_id = ?", g.ID).Count(&count)
	assert.Equal(t, int64(3), count)

	// Revision 3 should be a revert pointing to revision 1
	var rev model.GalgameRevision
	testDB.Where("galgame_id = ? AND revision = 3", g.ID).First(&rev)
	assert.Equal(t, "reverted", rev.Action)
	require.NotNil(t, rev.RevertedTo)
	assert.Equal(t, 1, *rev.RevertedTo)
}

func TestRevert_ForbiddenForOthers(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v10002", "test")

	err := testSvc.Revert(ctx, 999, g.ID, 1, []string{"user"})
	require.Error(t, err)
}

func TestSubmitPR(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v20001", "原始名")

	// Submit PR with a name change
	proposed := &model.Snapshot{
		VNDBID:   "v20001",
		NameZhCN: "PR修改后的名字",
	}

	pr, err := testSvc.SubmitPR(ctx, 2, g.ID, proposed, "修改名字", "")
	require.NoError(t, err)
	assert.Equal(t, 0, pr.Status) // pending
	assert.Equal(t, 1, pr.BaseRevision)
	assert.Equal(t, "修改名字", pr.Title)
}

func TestMergePR_Direct(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v20002", "原始名")

	// Submit PR
	proposed := &model.Snapshot{
		VNDBID:      "v20002",
		NameZhCN:    "PR修改",
		Aliases:     []string{},
		TagIDs:      []int{},
		OfficialIDs: []int{},
		EngineIDs:   []int{},
		Links:       []model.SnapshotLink{},
	}
	pr, err := testSvc.SubmitPR(ctx, 2, g.ID, proposed, "test", "")
	require.NoError(t, err)

	// Merge by creator (userID=1)
	err = testSvc.MergePR(ctx, 1, pr.GalgameID, pr.ID, []string{})
	require.NoError(t, err)

	// Verify galgame was updated
	var galgame model.Galgame
	testDB.First(&galgame, g.ID)
	assert.Equal(t, "PR修改", galgame.NameZhCN)

	// Verify PR status
	var updatedPR model.GalgamePR
	testDB.First(&updatedPR, pr.ID)
	assert.Equal(t, 1, updatedPR.Status) // merged
	assert.NotNil(t, updatedPR.RevisionID)

	// Verify revision created
	var rev model.GalgameRevision
	testDB.Where("galgame_id = ? AND revision = 2", g.ID).First(&rev)
	assert.Equal(t, "merged", rev.Action)
	assert.Equal(t, 2, rev.UserID) // PR author, not merger
}

func TestMergePR_AutoRebase(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v20003", "原始名")

	// Submit PR that changes name_zh_cn (based on revision 1)
	proposed := &model.Snapshot{
		VNDBID:      "v20003",
		NameZhCN:    "PR的名字",
		Aliases:     []string{},
		TagIDs:      []int{},
		OfficialIDs: []int{},
		EngineIDs:   []int{},
		Links:       []model.SnapshotLink{},
	}
	pr, err := testSvc.SubmitPR(ctx, 2, g.ID, proposed, "改名字", "")
	require.NoError(t, err)

	// Meanwhile, someone else updates name_en_us (creates revision 2)
	newEnName := "English Name"
	testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameEnUS: &newEnName})

	// PR's base_revision=1, latest=2, but fields don't conflict
	err = testSvc.MergePR(ctx, 1, pr.GalgameID, pr.ID, []string{})
	require.NoError(t, err)

	// Verify both changes are applied
	var galgame model.Galgame
	testDB.First(&galgame, g.ID)
	assert.Equal(t, "PR的名字", galgame.NameZhCN)        // from PR
	assert.Equal(t, "English Name", galgame.NameEnUS) // from direct edit, preserved by rebase
}

func TestMergePR_Conflict(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v20004", "原始名")

	// Submit PR that changes name_zh_cn
	proposed := &model.Snapshot{
		VNDBID:      "v20004",
		NameZhCN:    "PR的名字",
		Aliases:     []string{},
		TagIDs:      []int{},
		OfficialIDs: []int{},
		EngineIDs:   []int{},
		Links:       []model.SnapshotLink{},
	}
	pr, err := testSvc.SubmitPR(ctx, 2, g.ID, proposed, "改名字", "")
	require.NoError(t, err)

	// Meanwhile, someone else also changes name_zh_cn
	conflictName := "直接编辑的名字"
	testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &conflictName})

	// Merge should fail: both changed name_zh_cn
	err = testSvc.MergePR(ctx, 1, pr.GalgameID, pr.ID, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name_zh_cn")
}

func TestMergePR_AlreadyProcessed(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v20005", "test")

	proposed := &model.Snapshot{
		VNDBID:      "v20005",
		NameZhCN:    "changed",
		Aliases:     []string{},
		TagIDs:      []int{},
		OfficialIDs: []int{},
		EngineIDs:   []int{},
		Links:       []model.SnapshotLink{},
	}
	pr, _ := testSvc.SubmitPR(ctx, 2, g.ID, proposed, "test", "")

	// Merge once
	err := testSvc.MergePR(ctx, 1, pr.GalgameID, pr.ID, []string{})
	require.NoError(t, err)

	// Try to merge again
	err = testSvc.MergePR(ctx, 1, pr.GalgameID, pr.ID, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "已被处理")
}

func TestDeclinePR(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v20006", "test")

	proposed := &model.Snapshot{VNDBID: "v20006", NameZhCN: "changed"}
	pr, _ := testSvc.SubmitPR(ctx, 2, g.ID, proposed, "test", "")

	err := testSvc.DeclinePR(ctx, 1, pr.GalgameID, pr.ID, []string{})
	require.NoError(t, err)

	var updatedPR model.GalgamePR
	testDB.First(&updatedPR, pr.ID)
	assert.Equal(t, 2, updatedPR.Status) // declined
}

func TestListRevisions(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v30001", "test")

	// Create 2 more revisions
	name1 := "rev2"
	testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &name1})

	isMinor := true
	name2 := "rev3"
	testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &name2, IsMinor: &isMinor})

	// List all (3 revisions)
	items, total, err := testSvc.ListRevisions(ctx, g.ID, 1, 50, true)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, items, 3)

	// List excluding minor (2 revisions)
	items, total, err = testSvc.ListRevisions(ctx, g.ID, 1, 50, false)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, items, 2)
}

func TestGetRevisionDiff(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v30002", "原始名")

	name := "新名字"
	testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &name})

	changedKeys, oldSnap, newSnap, err := testSvc.GetRevisionDiff(ctx, g.ID, 2)
	require.NoError(t, err)
	assert.True(t, changedKeys["name_zh_cn"])
	assert.Equal(t, "原始名", oldSnap.NameZhCN)
	assert.Equal(t, "新名字", newSnap.NameZhCN)
}

// TestGetRevisionDiff_StaleBaselineDoesNotOverReport reproduces the kungal bug
// report: VNDB enrichment mutated the LIVE tables (added a screenshot, set bid)
// without minting a revision, leaving the previous revision's snapshot stale.
// A subsequent one-field user edit must diff to exactly that one field — the
// recorded changed_fields makes /diff immune to the stale baseline, instead of
// over-reporting screenshots/bid as "newly added".
func TestGetRevisionDiff_StaleBaselineDoesNotOverReport(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v30099", "原始名") // rev 1

	// Simulate post-creation enrichment straight into the live tables, with NO
	// revision (the historical root cause). rev 1's snapshot stays stale.
	require.NoError(t, testDB.Create(&model.GalgameScreenshot{
		GalgameID: g.ID, ImageHash: "deadbeefdeadbeef", SortOrder: 0, Source: "vndb",
	}).Error)
	require.NoError(t, testDB.Model(&model.Galgame{}).Where("id = ?", g.ID).
		Update("bid", 12345).Error)

	// User edits exactly one field. overlayUpdate diffs against live (which now
	// holds the screenshot + bid), so the recorded change set is {name_zh_cn}.
	name := "新名字"
	_, err := testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &name})
	require.NoError(t, err)

	changedKeys, _, _, err := testSvc.GetRevisionDiff(ctx, g.ID, 2)
	require.NoError(t, err)
	assert.True(t, changedKeys["name_zh_cn"], "the field the user actually edited")
	assert.False(t, changedKeys["screenshots"], "stale-baseline drift must NOT be reported")
	assert.False(t, changedKeys["bid"], "stale-baseline drift must NOT be reported")
	assert.Len(t, changedKeys, 1, "exactly one field changed")

	// Sanity: the edit revision recorded the precise change set.
	rev2, err := testSvc.GetRevision(ctx, g.ID, 2)
	require.NoError(t, err)
	assert.True(t, rev2.HasChangedFields())
	assert.Equal(t, []string{"name_zh_cn"}, rev2.ChangedFieldsList())
}
