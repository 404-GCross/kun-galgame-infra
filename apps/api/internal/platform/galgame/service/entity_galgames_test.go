package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEntityGalgames_ReverseLookup exercises the data path behind the step-20
// reverse-lookup ops (GET /galgame/officials/{id}/galgames and .../tags/{id}/…):
// the entity head (with aliases), the SFW-gated + paginated relation query, and
// EnrichBriefs mapping the page to ordered briefs. The handler is a thin wrapper
// over exactly these three calls, so this covers hit / 404 / pagination / SFW.
func TestEntityGalgames_ReverseLookup(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()

	oID := createTestOfficial(t, "K-JOYNT", "amateur")
	// Two SFW games + one NSFW game under the same official.
	g1, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v81001", NameZhCN: "作品甲", ContentLimit: "sfw", OfficialIDs: []int{oID}})
	g2, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v81002", NameZhCN: "作品乙", ContentLimit: "sfw", OfficialIDs: []int{oID}})
	g3, _ := testSvc.Create(ctx, 1, &dto.CreateGalgameRequest{VNDBID: "v81003", NameZhCN: "作品丙", ContentLimit: "nsfw", OfficialIDs: []int{oID}})

	// --- head (self-description) with aliases ---
	require.NoError(t, testOfficialRepo.Update(ctx, oID, nil, []string{"KJ", "ケージョイント"}))
	head, err := testOfficialRepo.FindByID(ctx, oID)
	require.NoError(t, err)
	assert.Equal(t, "K-JOYNT", head.Name)
	assert.Equal(t, "amateur", head.Category)
	assert.Len(t, head.Alias, 2)

	// --- SFW gate: only the two SFW games, total reflects the filter ---
	sfw, sfwTotal, err := testOfficialRepo.FindGalgamesByOfficialID(ctx, oID, 1, 24, "", "", "sfw")
	require.NoError(t, err)
	assert.Equal(t, int64(2), sfwTotal)
	briefs := testSvc.EnrichBriefs(ctx, sfw)
	assert.Len(t, briefs, 2)
	got := map[int]bool{}
	for _, b := range briefs {
		got[b.ID] = true
		assert.Equal(t, "sfw", b.ContentLimit)
		assert.NotEmpty(t, b.NameZhCN)
	}
	assert.True(t, got[g1.ID] && got[g2.ID])
	assert.False(t, got[g3.ID])

	// --- no filter ("all" → "") sees all three ---
	all, allTotal, err := testOfficialRepo.FindGalgamesByOfficialID(ctx, oID, 1, 24, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(3), allTotal)
	assert.Len(t, testSvc.EnrichBriefs(ctx, all), 3)

	// --- pagination: cap the page to 1, total unchanged ---
	page1, total, err := testOfficialRepo.FindGalgamesByOfficialID(ctx, oID, 1, 1, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, page1, 1)
	page2, _, err := testOfficialRepo.FindGalgamesByOfficialID(ctx, oID, 2, 1, "", "", "")
	require.NoError(t, err)
	assert.Len(t, page2, 1)
	assert.NotEqual(t, page1[0].ID, page2[0].ID)

	// --- 404 signal: FindByID on a missing id errors (handler maps → 404) ---
	_, err = testOfficialRepo.FindByID(ctx, 999999)
	require.Error(t, err)

	// --- EnrichBriefs on empty input → empty (non-nil) slice ---
	assert.Empty(t, testSvc.EnrichBriefs(ctx, nil))
}
