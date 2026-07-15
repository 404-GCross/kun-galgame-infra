package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"

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
	items, total, err := testSvc.ListDrafts(ctx, 1, 24, "")
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
	_, allTotal, err := testSvc.ListDrafts(ctx, 1, 24, "all")
	require.NoError(t, err)
	assert.Equal(t, int64(3), allTotal)

	// Limit clamp: page size 1 pages the set.
	page2, _, err := testSvc.ListDrafts(ctx, 2, 1, "")
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, d1.ID, page2[0].ID)
}
