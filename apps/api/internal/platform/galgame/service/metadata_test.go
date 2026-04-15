package service

import (
	"context"
	"strings"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ═══════════════════════════════════════════════════════════════════
// Tag Tests
// ═══════════════════════════════════════════════════════════════════

func TestTag_List(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	createTestTag(t, "RPG", "content")
	createTestTag(t, "Drama", "content")
	createTestTag(t, "Sexual Content", "sexual")

	items, total, err := testTagRepo.List(ctx, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, items, 3)
}

func TestTag_List_Pagination(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	for i := 0; i < 15; i++ {
		createTestTag(t, "tag_"+string(rune('A'+i)), "content")
	}

	items, total, err := testTagRepo.List(ctx, 1, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(15), total)
	assert.Len(t, items, 5)

	items2, _, err := testTagRepo.List(ctx, 3, 5)
	require.NoError(t, err)
	assert.Len(t, items2, 5)

	// Page beyond data
	items3, _, err := testTagRepo.List(ctx, 100, 5)
	require.NoError(t, err)
	assert.Len(t, items3, 0)
}

func TestTag_Search(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	tagID := createTestTag(t, "Role Playing Game", "content")
	createTestTag(t, "Drama", "content")

	// Add alias
	testDB.Create(&model.GalgameTagAlias{GalgameTagID: tagID, Name: "RPG"})

	// Search by name
	results, err := testTagRepo.Search(ctx, []string{"Drama"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "Drama", results[0].Name)

	// Search by alias
	results, err = testTagRepo.Search(ctx, []string{"RPG"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "Role Playing Game", results[0].Name)

	// Case insensitive
	results, err = testTagRepo.Search(ctx, []string{"drama"})
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// No match
	results, err = testTagRepo.Search(ctx, []string{"nonexistent"})
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestTag_Search_MultiTerm_AND(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	createTestTag(t, "Role Playing Game", "content")
	createTestTag(t, "Role Drama", "content")

	// Both terms must match → only "Role Playing Game" has "Role" AND "Playing"
	results, err := testTagRepo.Search(ctx, []string{"Role", "Playing"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "Role Playing Game", results[0].Name)
}

func TestTag_MultiTagFilter(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	tag1 := createTestTag(t, "RPG", "content")
	tag2 := createTestTag(t, "Drama", "content")
	tag3 := createTestTag(t, "Comedy", "content")

	g1, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v70001", TagIDs: []int{tag1, tag2}})
	testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v70002", TagIDs: []int{tag1, tag3}})
	testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v70003", TagIDs: []int{tag2, tag3}})

	// Filter by tag1 AND tag2 → only g1
	galgames, total, err := testTagRepo.FindGalgamesByMultipleTags(ctx, []int{tag1, tag2}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, g1.ID, galgames[0].ID)

	// Filter by tag1 only → g1 and g2
	galgames, total, err = testTagRepo.FindGalgamesByMultipleTags(ctx, []int{tag1}, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
}

func TestTag_Update_WithAliases(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	tagID := createTestTag(t, "OldName", "content")

	// Update name and set aliases
	err := testTagRepo.Update(ctx, tagID, map[string]any{"name": "NewName"}, []string{"Alias1", "Alias2"})
	require.NoError(t, err)

	tag, err := testTagRepo.FindByID(ctx, tagID)
	require.NoError(t, err)
	assert.Equal(t, "NewName", tag.Name)
	assert.Len(t, tag.Alias, 2)

	// Replace aliases
	err = testTagRepo.Update(ctx, tagID, nil, []string{"OnlyAlias"})
	require.NoError(t, err)

	tag, _ = testTagRepo.FindByID(ctx, tagID)
	assert.Len(t, tag.Alias, 1)
	assert.Equal(t, "OnlyAlias", tag.Alias[0].Name)
}

func TestTag_Update_EmptyAlias(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	tagID := createTestTag(t, "Test", "content")
	testDB.Create(&model.GalgameTagAlias{GalgameTagID: tagID, Name: "OldAlias"})

	// Pass empty alias list → should clear all aliases
	err := testTagRepo.Update(ctx, tagID, nil, []string{})
	require.NoError(t, err)

	tag, _ := testTagRepo.FindByID(ctx, tagID)
	assert.Len(t, tag.Alias, 0)
}

// ═══════════════════════════════════════════════════════════════════
// Official Tests
// ═══════════════════════════════════════════════════════════════════

func TestOfficial_List(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	createTestOfficial(t, "Developer A", "company")
	createTestOfficial(t, "Developer B", "individual")

	items, total, err := testOfficialRepo.List(ctx, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, items, 2)
}

func TestOfficial_Search(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	oID := createTestOfficial(t, "KEY Studio", "company")
	createTestOfficial(t, "Leaf", "company")
	testDB.Create(&model.GalgameOfficialAlias{GalgameOfficialID: oID, Name: "Key"})

	// Search by alias
	results, err := testOfficialRepo.Search(ctx, []string{"Key"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "KEY Studio", results[0].Name)
}

func TestOfficial_Update_WithAliases(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	oID := createTestOfficial(t, "TestDev", "company")

	err := testOfficialRepo.Update(ctx, oID,
		map[string]any{"link": "https://example.com"},
		[]string{"Alias1", "Alias2"},
	)
	require.NoError(t, err)

	o, _ := testOfficialRepo.FindByID(ctx, oID)
	assert.Equal(t, "https://example.com", o.Link)
	assert.Len(t, o.Alias, 2)
}

func TestOfficial_FindGalgames(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	oID := createTestOfficial(t, "TestDev", "company")
	g, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v80001", OfficialIDs: []int{oID}})

	galgames, total, err := testOfficialRepo.FindGalgamesByOfficialID(ctx, oID, 1, 10, "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, g.ID, galgames[0].ID)
}

// ═══════════════════════════════════════════════════════════════════
// Engine Tests
// ═══════════════════════════════════════════════════════════════════

func TestEngine_ListAll(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	createTestEngine(t, "Ren'Py")
	createTestEngine(t, "KiriKiri")

	items, err := testEngineRepo.ListAll(ctx)
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestEngine_Update(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	eID := createTestEngine(t, "OldEngine")

	err := testEngineRepo.Update(ctx, eID, map[string]any{
		"name":        "NewEngine",
		"description": "A game engine",
	})
	require.NoError(t, err)

	e, _ := testEngineRepo.FindByID(ctx, eID)
	assert.Equal(t, "NewEngine", e.Name)
	assert.Equal(t, "A game engine", e.Description)
}

func TestEngine_FindGalgames(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	eID := createTestEngine(t, "TestEngine")
	g, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v80010", EngineIDs: []int{eID}})

	galgames, total, err := testEngineRepo.FindGalgamesByEngineID(ctx, eID, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, g.ID, galgames[0].ID)
}

// ═══════════════════════════════════════════════════════════════════
// Series Tests
// ═══════════════════════════════════════════════════════════════════

func TestSeries_CreateAndList(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g1, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90001", NameZhCN: "游戏1"})
	g2, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90002", NameZhCN: "游戏2"})

	series := &model.GalgameSeries{Name: "测试系列", Description: "描述"}
	err := testSeriesRepo.Create(ctx, series, []int{g1.ID, g2.ID})
	require.NoError(t, err)
	assert.Greater(t, series.ID, 0)

	// Verify galgames are assigned
	var g model.Galgame
	testDB.First(&g, g1.ID)
	require.NotNil(t, g.SeriesID)
	assert.Equal(t, series.ID, *g.SeriesID)

	// List
	items, total, err := testSeriesRepo.List(ctx, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "测试系列", items[0].Name)
}

func TestSeries_Update_ReassignGalgames(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g1, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90010", NameZhCN: "游戏A"})
	g2, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90011", NameZhCN: "游戏B"})
	g3, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90012", NameZhCN: "游戏C"})

	series := &model.GalgameSeries{Name: "系列X"}
	testSeriesRepo.Create(ctx, series, []int{g1.ID, g2.ID})

	// Update: remove g1, add g3
	err := testSeriesRepo.Update(ctx, series.ID, map[string]any{"name": "系列Y"}, []int{g2.ID, g3.ID})
	require.NoError(t, err)

	// g1 should no longer be in the series
	var gal1 model.Galgame
	testDB.First(&gal1, g1.ID)
	assert.Nil(t, gal1.SeriesID)

	// g3 should be in the series
	var gal3 model.Galgame
	testDB.First(&gal3, g3.ID)
	require.NotNil(t, gal3.SeriesID)
	assert.Equal(t, series.ID, *gal3.SeriesID)
}

func TestSeries_Delete(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g1, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90020"})
	series := &model.GalgameSeries{Name: "ToDelete"}
	testSeriesRepo.Create(ctx, series, []int{g1.ID})

	err := testSeriesRepo.Delete(ctx, series.ID)
	require.NoError(t, err)

	// Galgame's series_id should be NULL
	var g model.Galgame
	testDB.First(&g, g1.ID)
	assert.Nil(t, g.SeriesID)

	// Series should be gone
	var count int64
	testDB.Model(&model.GalgameSeries{}).Where("id = ?", series.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestSeries_SearchGalgames(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90030", NameZhCN: "魔法少女"})
	testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90031", NameEnUS: "Magical Girl"})
	testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90032", NameZhCN: "完全不相关"})

	results, err := testSeriesRepo.SearchGalgames(ctx, []string{"魔法"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "魔法少女", results[0].NameZhCN)

	// Search by vndb_id
	results, err = testSeriesRepo.SearchGalgames(ctx, []string{"v90031"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestSeries_FindGalgamesByIDs(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g1, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90040"})
	g2, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v90041"})

	results, err := testSeriesRepo.FindGalgamesByIDs(ctx, []int{g1.ID, g2.ID})
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Empty IDs
	results, err = testSeriesRepo.FindGalgamesByIDs(ctx, []int{})
	require.NoError(t, err)
	assert.Len(t, results, 0)

	// Nonexistent IDs
	results, err = testSeriesRepo.FindGalgamesByIDs(ctx, []int{999999})
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

// ═══════════════════════════════════════════════════════════════════
// Link & Alias Tests (via GalgameService)
// ═══════════════════════════════════════════════════════════════════

func TestLink_CreateAndRevision(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g := createTestGalgame(t, "v60001", "test")

	// Create link in transaction with revision
	err := testGalgameRepo.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.GalgameLink{GalgameID: g.ID, UserID: 1, Name: "Steam", Link: "https://store.steampowered.com/app/123"}).Error; err != nil {
			return err
		}
		return testSvc.CreateRevisionFromCurrentState(tx, g.ID, 1, "updated", "添加链接: Steam", false)
	})
	require.NoError(t, err)

	// Should have revision 2
	var count int64
	testDB.Model(&model.GalgameRevision{}).Where("galgame_id = ?", g.ID).Count(&count)
	assert.Equal(t, int64(2), count)

	// Snapshot should contain the link
	rev, _ := testSvc.GetRevision(ctx, g.ID, 2)
	snap, _ := model.SnapshotFromJSON(rev.Snapshot)
	assert.Len(t, snap.Links, 2) // VNDB link + Steam link
}

func TestLink_Delete(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g := createTestGalgame(t, "v60002", "test")

	// Get the auto-created VNDB link
	var link model.GalgameLink
	testDB.Where("galgame_id = ?", g.ID).First(&link)

	err := testGalgameRepo.DB().Transaction(func(tx *gorm.DB) error {
		tx.Where("id = ? AND galgame_id = ?", link.ID, g.ID).Delete(&model.GalgameLink{})
		return testSvc.CreateRevisionFromCurrentState(tx, g.ID, 1, "updated", "删除链接", true)
	})
	require.NoError(t, err)

	// Snapshot should have no links
	rev, _ := testSvc.GetRevision(ctx, g.ID, 2)
	snap, _ := model.SnapshotFromJSON(rev.Snapshot)
	assert.Len(t, snap.Links, 0)
}

func TestAlias_CreateAndDelete(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	g := createTestGalgame(t, "v60003", "test")

	// Add alias
	err := testGalgameRepo.DB().Transaction(func(tx *gorm.DB) error {
		tx.Create(&model.GalgameAlias{GalgameID: g.ID, Name: "新别名"})
		return testSvc.CreateRevisionFromCurrentState(tx, g.ID, 1, "updated", "添加别名", false)
	})
	require.NoError(t, err)

	rev, _ := testSvc.GetRevision(ctx, g.ID, 2)
	snap, _ := model.SnapshotFromJSON(rev.Snapshot)
	assert.Contains(t, snap.Aliases, "新别名")

	// Delete alias
	var alias model.GalgameAlias
	testDB.Where("galgame_id = ? AND name = ?", g.ID, "新别名").First(&alias)

	err = testGalgameRepo.DB().Transaction(func(tx *gorm.DB) error {
		tx.Delete(&alias)
		return testSvc.CreateRevisionFromCurrentState(tx, g.ID, 1, "updated", "删除别名", true)
	})
	require.NoError(t, err)

	rev, _ = testSvc.GetRevision(ctx, g.ID, 3)
	snap, _ = model.SnapshotFromJSON(rev.Snapshot)
	assert.NotContains(t, snap.Aliases, "新别名")
}

// ═══════════════════════════════════════════════════════════════════
// Contributor Tests
// ═══════════════════════════════════════════════════════════════════

func TestContributor_AutoAdded(t *testing.T) {
	cleanTables(t)

	g := createTestGalgame(t, "v60010", "test")

	var count int64
	testDB.Model(&model.GalgameContributor{}).Where("galgame_id = ? AND user_id = 1", g.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestContributor_AddedOnUpdate(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v60011", "test")

	// User 2 (admin) edits
	name := "edited"
	testSvc.Update(ctx, 2, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &name})

	var count int64
	testDB.Model(&model.GalgameContributor{}).Where("galgame_id = ? AND user_id = 2", g.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestContributor_AddedOnPRMerge(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v60012", "test")

	proposed := &model.Snapshot{VNDBID: "v60012", NameZhCN: "PR edit",
		Aliases: []string{}, TagIDs: []int{}, OfficialIDs: []int{}, EngineIDs: []int{}, Links: []model.SnapshotLink{}}
	pr, _ := testSvc.SubmitPR(ctx, 3, g.ID, proposed, "test")
	testSvc.MergePR(ctx, 1, pr.ID, []string{})

	// User 3 should be a contributor now
	var count int64
	testDB.Model(&model.GalgameContributor{}).Where("galgame_id = ? AND user_id = 3", g.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestContributor_Delete(t *testing.T) {
	cleanTables(t)

	g := createTestGalgame(t, "v60013", "test")
	testDB.Create(&model.GalgameContributor{GalgameID: g.ID, UserID: 99})

	result := testDB.Where("galgame_id = ? AND user_id = ?", g.ID, 99).Delete(&model.GalgameContributor{})
	assert.Equal(t, int64(1), result.RowsAffected)
}

// ═══════════════════════════════════════════════════════════════════
// Galgame List & Get edge cases
// ═══════════════════════════════════════════════════════════════════

func TestGalgame_List_Empty(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	items, total, err := testSvc.List(ctx, &dto.ListGalgameRequest{Page: 1, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Len(t, items, 0)
}

func TestGalgame_List_Search(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v50001", NameZhCN: "魔法少女"})
	testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v50002", NameZhCN: "完全不同的游戏"})

	items, total, err := testSvc.List(ctx, &dto.ListGalgameRequest{Page: 1, Limit: 10, Search: "魔法"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "魔法少女", items[0].NameZhCN)
}

func TestGalgame_List_SortFieldMapping(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v50010"})
	testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v50011"})

	// resource_update_time is a valid sort field
	items, _, err := testSvc.List(ctx, &dto.ListGalgameRequest{Page: 1, Limit: 10, SortField: "resource_update_time", SortOrder: "desc"})
	require.NoError(t, err)
	assert.Len(t, items, 2)

	// Invalid sort field should fall back to "created"
	items, _, err = testSvc.List(ctx, &dto.ListGalgameRequest{Page: 1, Limit: 10, SortField: "nonexistent"})
	require.NoError(t, err)
	assert.Len(t, items, 2)

	// Rejected alias — "time" is not a valid sort field anymore
	items, _, err = testSvc.List(ctx, &dto.ListGalgameRequest{Page: 1, Limit: 10, SortField: "time"})
	require.NoError(t, err) // Falls back to "created"
	assert.Len(t, items, 2)

	// SQL injection attempt in sort field
	items, _, err = testSvc.List(ctx, &dto.ListGalgameRequest{Page: 1, Limit: 10, SortField: "id; DROP TABLE galgame;--"})
	require.NoError(t, err) // Falls back to "created", not crash
}

func TestGalgame_Get_BannedReturnsNotFound(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v50020"})
	testDB.Model(&model.Galgame{}).Where("id = ?", g.ID).Update("status", 1)

	_, _, err := testSvc.GetByID(ctx, g.ID)
	require.Error(t, err)
}

func TestGalgame_Get_Nonexistent(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	_, _, err := testSvc.GetByID(ctx, 999999)
	require.Error(t, err)
}

func TestGalgame_Create_AllRelations(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	tagID := createTestTag(t, "TestTag", "content")
	officialID := createTestOfficial(t, "TestDev", "company")
	engineID := createTestEngine(t, "TestEngine")

	g, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{
		VNDBID:      "v50030",
		NameZhCN:    "全关联测试",
		Aliases:     "别名1,别名2,别名3",
		TagIDs:      []int{tagID},
		OfficialIDs: []int{officialID},
		EngineIDs:   []int{engineID},
	})
	require.NoError(t, err)

	// Verify revision snapshot contains all relations
	var rev model.GalgameRevision
	testDB.Where("galgame_id = ? AND revision = 1", g.ID).First(&rev)
	snap, _ := model.SnapshotFromJSON(rev.Snapshot)

	assert.Len(t, snap.Aliases, 3)
	assert.Equal(t, []int{tagID}, snap.TagIDs)
	assert.Equal(t, []int{officialID}, snap.OfficialIDs)
	assert.Equal(t, []int{engineID}, snap.EngineIDs)
	assert.Len(t, snap.Links, 1) // Auto VNDB link
}

func TestGalgame_Create_EmptyAliases(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v50031", Aliases: ""})
	require.NoError(t, err)

	var count int64
	testDB.Model(&model.GalgameAlias{}).Where("galgame_id = ?", g.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestGalgame_Create_AliasesWithWhitespace(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v50032", Aliases: " name1 , name2 , , "})
	require.NoError(t, err)

	var aliases []model.GalgameAlias
	testDB.Where("galgame_id = ?", g.ID).Find(&aliases)
	assert.Len(t, aliases, 2)
	assert.Equal(t, "name1", aliases[0].Name)
	assert.Equal(t, "name2", aliases[1].Name)
}

func TestGalgame_Update_NoChanges(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v50033"})

	// Empty update → no new revision
	result, err := testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{})
	require.NoError(t, err)
	assert.Equal(t, g.ID, result.ID)

	var count int64
	testDB.Model(&model.GalgameRevision{}).Where("galgame_id = ?", g.ID).Count(&count)
	assert.Equal(t, int64(1), count) // Only the initial "created" revision
}

// ═══════════════════════════════════════════════════════════════════
// Revision edge cases
// ═══════════════════════════════════════════════════════════════════

func TestRevision_DiffOfFirstRevision(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v60020", "测试名")

	changedKeys, oldSnap, newSnap, err := testSvc.GetRevisionDiff(ctx, g.ID, 1)
	require.NoError(t, err)
	assert.True(t, changedKeys["vndb_id"])
	assert.Equal(t, "", oldSnap.VNDBID)
	assert.Equal(t, "v60020", newSnap.VNDBID)
}

func TestRevision_RevertToNonexistent(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v60021", "test")

	err := testSvc.Revert(ctx, 1, g.ID, 999, []string{"admin"})
	require.Error(t, err)
}

func TestRevision_MultipleUpdates_SequentialRevisions(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v60022", "v1")

	for i := 2; i <= 5; i++ {
		name := "v" + strings.Repeat("x", i)
		testSvc.Update(ctx, 1, g.ID, []string{"admin"}, &dto.UpdateGalgameRequest{NameZhCN: &name})
	}

	var revisions []model.GalgameRevision
	testDB.Where("galgame_id = ?", g.ID).Order("revision ASC").Find(&revisions)
	assert.Len(t, revisions, 5)

	// Verify sequential revision numbers
	for i, rev := range revisions {
		assert.Equal(t, i+1, rev.Revision)
	}
}
