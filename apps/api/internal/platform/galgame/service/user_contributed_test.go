package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/dto"

	"github.com/stretchr/testify/assert"
)

// TestListContributedGalgames verifies the contributed list (created OR edited,
// via galgame_contributor) is a superset of the created-only list: a user who
// only EDITED a galgame shows up in /contributed but not in /galgames.
func TestListContributedGalgames(t *testing.T) {
	cleanTables(t)
	ctx := context.Background()

	g := createTestGalgame(t, "v70001", "contrib-test") // created by user 1

	// User 2 (admin) edits it → recorded as a contributor without creating it.
	name := "edited by user 2"
	_, err := testSvc.Update(ctx, 2, g.ID, []string{"admin"},
		&dto.UpdateGalgameRequest{NameZhCN: &name})
	assert.NoError(t, err)

	// Editor (user 2): present in /contributed, absent from /galgames (created-only).
	contributed, total, err := testSvc.ListContributedGalgames(ctx, 2, 1, 24, "")
	assert.NoError(t, err)
	assert.Equal(t, int64(1), total)
	if assert.Len(t, contributed, 1) {
		assert.Equal(t, g.ID, contributed[0].ID)
	}

	created, createdTotal, err := testSvc.ListUserGalgames(ctx, 2, 1, 24, "")
	assert.NoError(t, err)
	assert.Equal(t, int64(0), createdTotal)
	assert.Len(t, created, 0)

	// Creator (user 1): present in /contributed (auto-added on create).
	c1, t1, err := testSvc.ListContributedGalgames(ctx, 1, 1, 24, "")
	assert.NoError(t, err)
	assert.Equal(t, int64(1), t1)
	assert.Len(t, c1, 1)
}
