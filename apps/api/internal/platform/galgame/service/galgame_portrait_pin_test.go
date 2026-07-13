package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// portrait-pin hashes (64 hex chars, like the other cover tests).
const (
	hashPortA = "1111111111111111111111111111111111111111111111111111111111111111"
	hashPortB = "2222222222222222222222222222222222222222222222222222222222222222"
)

// TestGalgameCover_PortraitPinPartialUnique verifies the partial unique index
// idx_galgame_cover_portrait_pinned enforces "at most one pinned portrait per
// galgame" — the vertical companion to idx_galgame_cover_pinned. Independent of
// sort_order (a portrait pin never touches sort_order).
func TestGalgameCover_PortraitPinPartialUnique(t *testing.T) {
	cleanTables(t)
	getRepos()
	gid := makeGalgame(t)

	// First portrait_pinned row is fine (sort_order non-zero — a portrait pin
	// is not the landscape banner).
	require.NoError(t, testDB.Create(&model.GalgameCover{
		GalgameID: gid, ImageHash: hashPortA, SortOrder: 1, PortraitPinned: true,
	}).Error)

	// Second portrait_pinned row for the SAME galgame must be rejected.
	err := testDB.Create(&model.GalgameCover{
		GalgameID: gid, ImageHash: hashPortB, SortOrder: 2, PortraitPinned: true,
	}).Error
	require.Error(t, err, "two portrait_pinned covers must violate the partial unique index")

	// A non-pinned row for the same galgame is fine (index is partial).
	require.NoError(t, testDB.Create(&model.GalgameCover{
		GalgameID: gid, ImageHash: hashPortB, SortOrder: 2, PortraitPinned: false,
	}).Error)
}

// TestEffectivePortrait_DetailPath verifies the FindByID detail path resolves
// effective_portrait_hash from the portrait_pinned row, and leaves it nil when
// no portrait is pinned (no landscape fallback).
func TestEffectivePortrait_DetailPath(t *testing.T) {
	cleanTables(t)
	getRepos()
	ctx := context.Background()
	gid := makeGalgame(t)

	// Landscape pin (sort_order=0) + a non-pinned portrait candidate.
	require.NoError(t, testDB.Create(&model.GalgameCover{
		GalgameID: gid, ImageHash: hashPortA, SortOrder: 0,
	}).Error)
	require.NoError(t, testDB.Create(&model.GalgameCover{
		GalgameID: gid, ImageHash: hashPortB, SortOrder: 1,
	}).Error)

	// No portrait pinned yet → effective_portrait_hash is nil (no fallback),
	// while effective_banner_hash resolves to the sort_order=0 row.
	g, err := testGalgameRepo.FindByID(ctx, gid)
	require.NoError(t, err)
	require.NotNil(t, g.EffectiveBannerHash)
	assert.Equal(t, hashPortA, *g.EffectiveBannerHash)
	assert.Nil(t, g.EffectivePortraitHash, "no portrait pin → nil, no landscape fallback")

	// Pin the portrait candidate.
	require.NoError(t, testDB.Model(&model.GalgameCover{}).
		Where("galgame_id = ? AND image_hash = ?", gid, hashPortB).
		Update("portrait_pinned", true).Error)

	g, err = testGalgameRepo.FindByID(ctx, gid)
	require.NoError(t, err)
	require.NotNil(t, g.EffectivePortraitHash, "portrait pin present → resolved")
	assert.Equal(t, hashPortB, *g.EffectivePortraitHash)
	// Landscape pin unchanged (the two pins are independent).
	require.NotNil(t, g.EffectiveBannerHash)
	assert.Equal(t, hashPortA, *g.EffectiveBannerHash)
}
