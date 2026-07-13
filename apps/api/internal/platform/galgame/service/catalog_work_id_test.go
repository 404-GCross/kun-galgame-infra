package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDetailCarriesCatalogWorkID pins the step-34 T1 detail passthrough: a
// galgame with catalog_work_id set surfaces it in the single detail (via
// FindByID SELECT * + NewGalgameDetail) and the batch detail (detailColumns +
// BatchDetailWithViewer), and a galgame without it omits the field.
func TestDetailCarriesCatalogWorkID(t *testing.T) {
	if testDB == nil {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	cleanTables(t)
	ctx := context.Background()

	g := &model.Galgame{VNDBID: "v777", UserID: 1, Status: 0}
	require.NoError(t, testDB.Create(g).Error)
	cwid := int64(90001)
	// The write/create path never sets the pointer — reconcile does. Simulate it.
	require.NoError(t, testDB.Model(&model.Galgame{}).Where("id = ?", g.ID).
		Update("catalog_work_id", cwid).Error)

	// single detail
	got, _, err := testSvc.GetByIDWithViewer(ctx, g.ID, 0, nil, "")
	require.NoError(t, err)
	require.NotNil(t, got.CatalogWorkID)
	assert.Equal(t, cwid, *got.CatalogWorkID)
	d := dto.NewGalgameDetail(got)
	require.NotNil(t, d.CatalogWorkID)
	assert.Equal(t, cwid, *d.CatalogWorkID)

	// batch view=detail
	briefs, err := testSvc.BatchDetailWithViewer(ctx, []int{g.ID}, 0, "")
	require.NoError(t, err)
	require.Len(t, briefs, 1)
	require.NotNil(t, briefs[0].CatalogWorkID)
	assert.Equal(t, cwid, *briefs[0].CatalogWorkID)

	// a galgame without a claim omits the field (nil pointer).
	g2 := &model.Galgame{VNDBID: "v778", UserID: 1, Status: 0}
	require.NoError(t, testDB.Create(g2).Error)
	got2, _, err := testSvc.GetByIDWithViewer(ctx, g2.ID, 0, nil, "")
	require.NoError(t, err)
	assert.Nil(t, got2.CatalogWorkID)
}
