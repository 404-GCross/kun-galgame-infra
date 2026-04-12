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

	// Verify revision 1 was created
	var rev model.GalgameRevision
	err = testDB.Where("galgame_id = ? AND revision = 1", galgame.ID).First(&rev).Error
	require.NoError(t, err)
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

	// Verify snapshot includes the tag
	var rev model.GalgameRevision
	testDB.Where("galgame_id = ? AND revision = 1", galgame.ID).First(&rev)
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

	// Should have revision 2
	var rev model.GalgameRevision
	err = testDB.Where("galgame_id = ? AND revision = 2", galgame.ID).First(&rev).Error
	require.NoError(t, err)
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
