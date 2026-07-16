package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListDrafts covers the claim-funnel browser's data path (GET
// /galgame/drafts): only status=2 rows, newest first, the sfw content gate,
// and the limit clamp.
func TestListDrafts(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	published, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v82001", NameZhCN: "已发布", ContentLimit: "sfw"})
	require.NoError(t, err)
	d1, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v82002", NameJaJP: "ドラフト壱", ContentLimit: "sfw"})
	require.NoError(t, err)
	d2, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v82003", NameJaJP: "ドラフト弐", ContentLimit: "sfw"})
	require.NoError(t, err)
	dNSFW, err := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v82004", NameJaJP: "ドラフト参", ContentLimit: "nsfw"})
	require.NoError(t, err)
	// Demote the three drafts to status=2 (Create publishes at status=0).
	require.NoError(t, testDB.Exec(`UPDATE galgame SET status = 2 WHERE id IN ?`, []int{d1.ID, d2.ID, dNSFW.ID}).Error)

	// SFW face: only the two sfw drafts, newest (higher id) first; the
	// published row never appears.
	items, total, err := testSvc.ListDrafts(ctx, 1, 24, "", repository.DraftFilters{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 2)
	assert.Equal(t, d2.ID, items[0].ID)
	assert.Equal(t, d1.ID, items[1].ID)
	for _, g := range items {
		assert.NotEqual(t, published.ID, g.ID)
		assert.Equal(t, 2, g.Status)
	}

	// content_limit=all surfaces the nsfw draft too.
	_, allTotal, err := testSvc.ListDrafts(ctx, 1, 24, "all", repository.DraftFilters{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), allTotal)

	// Limit clamp: page size 1 pages the set.
	page2, _, err := testSvc.ListDrafts(ctx, 2, 1, "", repository.DraftFilters{})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, d1.ID, page2[0].ID)

	// Entity scoping: an official attached to d1 only → scoped list = {d1};
	// a different official → empty. Same machinery serves tag_id/engine_id.
	oID := createTestOfficial(t, "DraftBrand", "amateur")
	require.NoError(t, testDB.Exec(`INSERT INTO galgame_official_relation (galgame_id, official_id) VALUES (?, ?)`, d1.ID, oID).Error)
	scoped, scopedTotal, err := testSvc.ListDrafts(ctx, 1, 24, "", repository.DraftFilters{OfficialID: oID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), scopedTotal)
	require.Len(t, scoped, 1)
	assert.Equal(t, d1.ID, scoped[0].ID)
	other := createTestOfficial(t, "OtherBrand", "amateur")
	_, emptyTotal, err := testSvc.ListDrafts(ctx, 1, 24, "", repository.DraftFilters{OfficialID: other})
	require.NoError(t, err)
	assert.Zero(t, emptyTotal)
}
