package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreate_WithRevision(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	galgame, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{
		VNDBID:   "v12345",
		NameZhCN: "测试游戏",
		NameEnUS: "Test Game",
		Aliases:  "别名1,别名2",
	})
	require.NoError(t, err)
	require.NotNil(t, galgame)
	assert.Equal(t, "v12345", galgame.VNDBID)

	// Verify revision 1 was created (E2a: engine log, read via the bridge)
	rev := bridgeRevision(t, galgame.ID, 1)
	assert.Equal(t, "created", rev.Action)
	assert.Equal(t, 1, rev.UserID)

	// Verify snapshot contains the data
	snapshot, err := model.SnapshotFromJSON(rev.Snapshot)
	require.NoError(t, err)
	assert.Equal(t, "v12345", snapshot.VNDBID)
	assert.Equal(t, "测试游戏", snapshot.NameZhCN)
	assert.Contains(t, snapshot.Aliases, "别名1")
	assert.Contains(t, snapshot.Aliases, "别名2")

	// Verify VNDB link was created
	assert.Len(t, snapshot.Links, 1)
	assert.Equal(t, "VNDB", snapshot.Links[0].Name)

	// Verify contributor was added
	var count int64
	testDB.Model(&model.GalgameContributor{}).Where("galgame_id = ? AND user_id = 1", galgame.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestCreate_DuplicateVNDB(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	_, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{
		VNDBID:   "v11111",
		NameZhCN: "第一个",
	})
	require.NoError(t, err)

	_, err = testSvc.Create(ctx, 2, &dto.CreateGalgameRequest{
		VNDBID:   "v11111",
		NameZhCN: "重复的",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20004") // ErrGalgameVNDBExists
}

func TestCreate_InvalidVNDB(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	_, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{
		VNDBID: "invalid",
	})
	require.Error(t, err)
}

func TestCreate_WithTags(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	tagID := createTestTag(t, "RPG", "content")

	galgame, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{
		VNDBID: "v22222",
		TagIDs: []int{tagID},
	})
	require.NoError(t, err)

	// Verify tag relation exists
	var rel model.GalgameTagRelation
	err = testDB.Where("galgame_id = ? AND tag_id = ?", galgame.ID, tagID).First(&rel).Error
	require.NoError(t, err)

	// Verify snapshot includes the tag (E2a: bridge read)
	rev := bridgeRevision(t, galgame.ID, 1)
	snapshot, _ := model.SnapshotFromJSON(rev.Snapshot)
	assert.Contains(t, snapshot.TagIDs, tagID)
}

func TestUpdate_CreatesRevision(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	galgame, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{
		VNDBID:   "v33333",
		NameZhCN: "原始名",
	})
	require.NoError(t, err)

	newName := "修改后的名字"
	_, err = testSvc.Update(ctx, 1, galgame.ID, []string{"admin"}, &dto.UpdateGalgameRequest{
		NameZhCN: &newName,
	})
	require.NoError(t, err)

	// Should have revision 2 (E2a: engine direct edit reads back as "updated")
	rev := bridgeRevision(t, galgame.ID, 2)
	assert.Equal(t, "updated", rev.Action)

	snapshot, _ := model.SnapshotFromJSON(rev.Snapshot)
	assert.Equal(t, "修改后的名字", snapshot.NameZhCN)
}

func TestUpdate_ForbiddenForOthers(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	galgame, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{
		VNDBID:   "v44444",
		NameZhCN: "原始",
	})
	require.NoError(t, err)

	newName := "被修改"
	_, err = testSvc.Update(ctx, 999, galgame.ID, []string{"user"}, &dto.UpdateGalgameRequest{
		NameZhCN: &newName,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20005") // ErrGalgameForbidden
}

func TestUpdate_AdminCanEdit(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	galgame, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{
		VNDBID:   "v55555",
		NameZhCN: "原始",
	})
	require.NoError(t, err)

	newName := "管理员修改"
	_, err = testSvc.Update(ctx, 999, galgame.ID, []string{"admin"}, &dto.UpdateGalgameRequest{
		NameZhCN: &newName,
	})
	require.NoError(t, err)
}

// finding #21: Update must reject a malformed vndb_id instead of silently
// persisting it (Create/Submit already reject this).
func TestUpdate_RejectsInvalidVNDB(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	g := createTestGalgame(t, "v60001", "原始")

	bad := "garbage12"
	_, err := testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{VNDBID: &bad})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20003") // ErrGalgameInvalidVNDB
}

// finding #21: changing vndb_id to one already used by another galgame must
// return the actionable 20004 rather than a raw 500 from the unique index.
func TestUpdate_RejectsDuplicateVNDB(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	createTestGalgame(t, "v60002", "甲")
	g2 := createTestGalgame(t, "v60003", "乙")

	dup := "v60002"
	_, err := testSvc.Update(ctx, 1, g2.ID, []string{"admin"}, &dto.UpdateGalgameRequest{VNDBID: &dup})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20004") // ErrGalgameVNDBExists
}

// finding #21: re-saving a galgame's OWN current vndb_id must not false-
// positive as a duplicate.
func TestUpdate_SameVNDBIsAllowed(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	g := createTestGalgame(t, "v60004", "原始")

	same, newName := "v60004", "改名"
	_, err := testSvc.Update(ctx, 1, g.ID, []string{"admin"},
		&dto.UpdateGalgameRequest{VNDBID: &same, NameZhCN: &newName})
	require.NoError(t, err)
}

// finding #38: a non-admin owner must not direct-edit (PUT) a draft (status
// 3/4) — those go through PatchDraft; admins still can.
func TestUpdate_RejectsDraftForNonAdmin(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()
	g := createTestGalgame(t, "v60005", "原始")
	require.NoError(t, testDB.Model(&model.Galgame{}).Where("id = ?", g.ID).
		Update("status", model.GalgameStatusPending).Error)

	newName := "改名"
	_, err := testSvc.Update(ctx, 1, g.ID, []string{"user"}, &dto.UpdateGalgameRequest{NameZhCN: &newName})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20008") // ErrGalgameDraftStatusInvalid

	// Admin keeps direct-edit on a non-published entry.
	_, err = testSvc.Update(ctx, 999, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &newName})
	require.NoError(t, err)
}

func TestCheckVNDB(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	exists, _, err := testSvc.CheckVNDB(ctx, "v99999")
	require.NoError(t, err)
	assert.False(t, exists)

	_, err = testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{
		VNDBID: "v99999",
	})
	require.NoError(t, err)

	exists, id, err := testSvc.CheckVNDB(ctx, "v99999")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Greater(t, id, 0)
}
