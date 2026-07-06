package bangumicommon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spot-check values are pinned to the embedded snapshot (see data/README.md);
// they must be updated when the snapshot is refreshed and an entry changed.

func TestLoadStaffPositions(t *testing.T) {
	staffs, err := LoadStaffPositions()
	require.NoError(t, err)

	total := 0
	for _, positions := range staffs {
		total += len(positions)
	}
	// 246 at snapshot time; assert a floor so a refreshed snapshot with a few
	// upstream removals still passes, while a broken load fails loudly.
	assert.GreaterOrEqual(t, total, 200)

	dev := staffs[SubjectTypeGame][1001]
	assert.Equal(t, "Developer", dev.EN)
	assert.Equal(t, "开发", dev.CN)
	assert.Equal(t, "開発元", dev.JP)
	require.Len(t, dev.Categories, 1)
	assert.Equal(t, "发行类", dev.Categories[0].CN)
}

func TestLoadRelations(t *testing.T) {
	relations, err := LoadRelations()
	require.NoError(t, err)
	assert.NotEmpty(t, relations)

	adaptation := relations[SubjectTypeGame][1]
	assert.Equal(t, "Adaptation", adaptation.EN)
	assert.Equal(t, "改编", adaptation.CN)
	assert.NotEmpty(t, adaptation.Desc)
}

func TestLoadPlatforms(t *testing.T) {
	platforms, err := LoadPlatforms()
	require.NoError(t, err)
	assert.NotEmpty(t, platforms)

	games := platforms["4"][4001]
	assert.Equal(t, Platform{ID: 4001, Type: "games", TypeCN: "游戏", Alias: "games"}, games)

	pc := platforms["game_platforms"][4]
	assert.Equal(t, "PC", pc.Type)
}
