package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTakeSnapshot(t *testing.T) {
	g := &Galgame{
		VNDBID:           "v12345",
		NameEnUS:         "Test Game",
		NameJaJP:         "テストゲーム",
		NameZhCN:         "测试游戏",
		NameZhTW:         "測試遊戲",
		Banner:           "https://example.com/banner.webp",
		IntroEnUS:        "English intro",
		IntroJaJP:        "日本語紹介",
		IntroZhCN:        "中文简介",
		IntroZhTW:        "中文簡介",
		ContentLimit:     "sfw",
		OriginalLanguage: "ja-jp",
		AgeLimit:         "r18",
		Alias: []GalgameAlias{
			{Name: "别名B"},
			{Name: "别名A"},
		},
		Tag: []GalgameTagRelation{
			{TagID: 3},
			{TagID: 1},
		},
		Official: []GalgameOfficialRelation{
			{OfficialID: 2},
		},
		Engine: []GalgameEngineRelation{
			{EngineID: 5},
		},
		Link: []GalgameLink{
			{Name: "VNDB", Link: "https://vndb.org/v12345"},
		},
	}

	s := TakeSnapshot(g)

	assert.Equal(t, "v12345", s.VNDBID)
	assert.Equal(t, "Test Game", s.NameEnUS)
	assert.Equal(t, "sfw", s.ContentLimit)

	// Aliases should be sorted
	assert.Equal(t, []string{"别名A", "别名B"}, s.Aliases)

	// Tag IDs should be sorted
	assert.Equal(t, []int{1, 3}, s.TagIDs)

	assert.Equal(t, []int{2}, s.OfficialIDs)
	assert.Equal(t, []int{5}, s.EngineIDs)
	assert.Equal(t, []SnapshotLink{{Name: "VNDB", Link: "https://vndb.org/v12345"}}, s.Links)
}

func TestTakeSnapshot_Empty(t *testing.T) {
	g := &Galgame{}
	s := TakeSnapshot(g)

	assert.Empty(t, s.Aliases)
	assert.Empty(t, s.TagIDs)
	assert.Empty(t, s.OfficialIDs)
	assert.Empty(t, s.EngineIDs)
	assert.Empty(t, s.Links)
	// Slices should be non-nil empty (for deterministic JSON)
	assert.NotNil(t, s.Aliases)
	assert.NotNil(t, s.TagIDs)
}

func TestSnapshotJSON_Roundtrip(t *testing.T) {
	original := &Snapshot{
		VNDBID:      "v999",
		NameZhCN:    "测试",
		ContentLimit: "nsfw",
		Aliases:     []string{"a", "b"},
		TagIDs:      []int{1, 2, 3},
		Links:       []SnapshotLink{{Name: "VNDB", Link: "https://vndb.org/v999"}},
	}

	data, err := original.ToJSON()
	require.NoError(t, err)

	restored, err := SnapshotFromJSON(data)
	require.NoError(t, err)

	assert.Equal(t, original.VNDBID, restored.VNDBID)
	assert.Equal(t, original.NameZhCN, restored.NameZhCN)
	assert.Equal(t, original.Aliases, restored.Aliases)
	assert.Equal(t, original.TagIDs, restored.TagIDs)
	assert.Equal(t, original.Links, restored.Links)
}

func TestChangedKeys_NoChange(t *testing.T) {
	s := &Snapshot{
		VNDBID:   "v1",
		NameZhCN: "name",
		TagIDs:   []int{1, 2},
		Aliases:  []string{"a"},
	}
	keys := ChangedKeys(s, s)
	assert.Empty(t, keys)
}

func TestChangedKeys_ScalarChange(t *testing.T) {
	old := &Snapshot{
		VNDBID:   "v1",
		NameZhCN: "旧名",
		NameEnUS: "same",
		TagIDs:   []int{1, 2},
		Aliases:  []string{},
		Links:    []SnapshotLink{},
	}
	new := &Snapshot{
		VNDBID:   "v1",
		NameZhCN: "新名",
		NameEnUS: "same",
		TagIDs:   []int{1, 2},
		Aliases:  []string{},
		Links:    []SnapshotLink{},
	}

	keys := ChangedKeys(old, new)
	assert.True(t, keys["name_zh_cn"])
	assert.False(t, keys["vndb_id"])
	assert.False(t, keys["name_en_us"])
	assert.False(t, keys["tag_ids"])
	assert.Len(t, keys, 1)
}

func TestChangedKeys_ArrayChange(t *testing.T) {
	old := &Snapshot{
		TagIDs:  []int{1, 2},
		Aliases: []string{"a"},
		Links:   []SnapshotLink{{Name: "VNDB", Link: "https://vndb.org/v1"}},
	}
	new := &Snapshot{
		TagIDs:  []int{1, 2, 3},
		Aliases: []string{"a", "b"},
		Links:   []SnapshotLink{{Name: "VNDB", Link: "https://vndb.org/v1"}},
	}

	keys := ChangedKeys(old, new)
	assert.True(t, keys["tag_ids"])
	assert.True(t, keys["aliases"])
	assert.False(t, keys["links"])
}

func TestChangedKeys_SeriesIDNil(t *testing.T) {
	seriesID := 5
	old := &Snapshot{SeriesID: nil}
	new := &Snapshot{SeriesID: &seriesID}

	keys := ChangedKeys(old, new)
	assert.True(t, keys["series_id"])
}

func TestApplyChanges_Partial(t *testing.T) {
	target := &Snapshot{
		VNDBID:   "v1",
		NameZhCN: "目标名",
		NameEnUS: "target",
		TagIDs:   []int{1, 2},
		Aliases:  []string{"old"},
	}
	source := &Snapshot{
		VNDBID:   "v1",
		NameZhCN: "源名",
		NameEnUS: "source",
		TagIDs:   []int{3, 4},
		Aliases:  []string{"new"},
	}

	// Only apply name_zh_cn and tag_ids
	changedKeys := map[string]bool{
		"name_zh_cn": true,
		"tag_ids":    true,
	}

	ApplyChanges(target, source, changedKeys)

	assert.Equal(t, "源名", target.NameZhCN)       // Applied
	assert.Equal(t, "target", target.NameEnUS)       // NOT applied
	assert.Equal(t, []int{3, 4}, target.TagIDs)      // Applied
	assert.Equal(t, []string{"old"}, target.Aliases)  // NOT applied
}

func TestApplyChanges_AllFields(t *testing.T) {
	seriesID := 10
	target := &Snapshot{}
	source := &Snapshot{
		VNDBID:           "v999",
		NameEnUS:         "en",
		NameJaJP:         "ja",
		NameZhCN:         "cn",
		NameZhTW:         "tw",
		Banner:           "banner",
		IntroEnUS:        "intro_en",
		IntroJaJP:        "intro_ja",
		IntroZhCN:        "intro_cn",
		IntroZhTW:        "intro_tw",
		ContentLimit:     "nsfw",
		OriginalLanguage: "en-us",
		AgeLimit:         "all",
		SeriesID:         &seriesID,
		Aliases:          []string{"a"},
		TagIDs:           []int{1},
		OfficialIDs:      []int{2},
		EngineIDs:        []int{3},
		Links:            []SnapshotLink{{Name: "test", Link: "https://test.com"}},
	}

	// Apply all fields
	allKeys := ChangedKeys(&Snapshot{}, source)
	ApplyChanges(target, source, allKeys)

	assert.Equal(t, source.VNDBID, target.VNDBID)
	assert.Equal(t, source.NameEnUS, target.NameEnUS)
	assert.Equal(t, source.ContentLimit, target.ContentLimit)
	assert.Equal(t, source.SeriesID, target.SeriesID)
	assert.Equal(t, source.Aliases, target.Aliases)
	assert.Equal(t, source.TagIDs, target.TagIDs)
	assert.Equal(t, source.OfficialIDs, target.OfficialIDs)
	assert.Equal(t, source.Links, target.Links)
}
