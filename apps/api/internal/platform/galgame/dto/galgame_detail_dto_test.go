package dto

import (
	"encoding/json"
	"testing"
	"time"

	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// TestGalgameDetail_WireIdenticalToModel pins GalgameDetail to the JSON a
// FindByID-preloaded model.Galgame marshals to (every detail relation loaded,
// covers/screenshots dims-enriched, EffectiveBannerHash set). PR/History are NOT
// preloaded by the detail path, so they're left nil here (absent on the wire) —
// mirroring reality. A pass proves the handler swap is a non-behaviour change.
func TestGalgameDetail_WireIdenticalToModel(t *testing.T) {
	ts := model.Timestamp(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	rd := model.Date(time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC))
	bid := 555
	sid := 7
	cwid := int64(80123)
	g := &model.Galgame{
		ID: 1, VNDBID: "v54934", BangumiID: &bid, CatalogWorkID: &cwid,
		ReleaseDate: &rd, ReleaseDateTBA: false, ReleasePrecision: "day",
		NameEnUS: "Name", NameJaJP: "名前", NameZhCN: "名字", NameZhTW: "名字",
		Banner:    "https://t.vndb.org/cv/00/1.jpg",
		IntroEnUS: "intro-en", IntroJaJP: "intro-ja", IntroZhCN: "intro-zh", IntroZhTW: "intro-tw",
		ContentLimit: "sfw", Status: 0, View: 100,
		ResourceUpdateTime: ts, OriginalLanguage: "ja-jp", AgeLimit: "r18",
		UserID: 1, SeriesID: &sid, Created: ts, Updated: ts,
		Series: &model.GalgameSeries{ID: 7, Name: "Series", Description: "d", Created: ts, Updated: ts, GalgameCount: 3},
		Alias: []model.GalgameAlias{
			{ID: 10, Name: "alias-a", GalgameID: 1, Created: ts, Updated: ts},
		},
		Link: []model.GalgameLink{
			{ID: 20, Name: "官网", Link: "https://x", Source: "vndb", SourceKey: "k", GalgameID: 1, UserID: 1, Created: ts, Updated: ts},
		},
		Contributor: []model.GalgameContributor{
			{ID: 30, GalgameID: 1, UserID: 2, Created: ts, Updated: ts},
		},
		Tag: []model.GalgameTagRelation{
			{
				GalgameID: 1, TagID: 40, SpoilerLevel: 1, Source: "vndb", Created: ts, Updated: ts,
				Tag: &model.GalgameTag{ID: 40, Name: "校园", Category: "content", Description: "d", Created: ts, Updated: ts, GalgameCount: 9},
			},
		},
		Official: []model.GalgameOfficialRelation{
			{
				GalgameID: 1, OfficialID: 144, Source: "vndb", Created: ts, Updated: ts,
				Official: &model.GalgameOfficial{
					ID: 144, Name: "オトメイト", Original: "Otomate", Link: "https://otomate.jp",
					Category: "company", Lang: "ja", Description: "", Created: ts, Updated: ts, GalgameCount: 42,
					Alias: []model.GalgameOfficialAlias{
						{ID: 50, Name: "OM", GalgameOfficialID: 144, Created: ts, Updated: ts},
					},
				},
			},
		},
		Engine: []model.GalgameEngineRelation{
			{
				GalgameID: 1, EngineID: 60, Created: ts, Updated: ts,
				Engine: &model.GalgameEngine{
					ID: 60, Name: "RealLive", Description: "d",
					Alias: datatypes.JSON([]byte(`["rl","reallive"]`)), Created: ts, Updated: ts, GalgameCount: 5,
				},
			},
		},
		Cover: []model.GalgameCover{
			{GalgameID: 1, ImageHash: "h0", SortOrder: 0, Source: "vndb", SourceKey: "cv1", Kind: "main", Created: ts, Width: 800, Height: 600, Thumbhash: "TH"},
		},
		Screenshot: []model.GalgameScreenshot{
			{GalgameID: 1, ImageHash: "s0", SortOrder: 0, Caption: "cg", Source: "vndb", SourceKey: "sc1", Created: ts, Width: 1920, Height: 1080, Thumbhash: "SH"},
		},
	}
	model.PopulateEffectiveBanner(g)

	oldJSON, err := json.Marshal(g)
	require.NoError(t, err)
	newJSON, err := json.Marshal(NewGalgameDetail(g))
	require.NoError(t, err)

	assert.JSONEq(t, string(oldJSON), string(newJSON),
		"GalgameDetail must be wire-identical to the FindByID-preloaded model.Galgame")
}

// TestGalgameDetail_WireIdenticalToListSubset covers GET /galgame (list), whose
// items are a lighter preload subset (Tag.Tag / Official.Official (no alias) /
// Cover) — the un-preloaded relations must be absent, matching the raw model.
func TestGalgameDetail_WireIdenticalToListSubset(t *testing.T) {
	ts := model.Timestamp(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	g := &model.Galgame{
		ID: 2, VNDBID: "v1", NameZhCN: "列表项", Banner: "b", ContentLimit: "sfw",
		ReleasePrecision: "day", OriginalLanguage: "ja-jp", AgeLimit: "r18",
		UserID: 1, Created: ts, Updated: ts, ResourceUpdateTime: ts,
		Tag: []model.GalgameTagRelation{
			{GalgameID: 2, TagID: 40, Source: "vndb", Created: ts, Updated: ts,
				Tag: &model.GalgameTag{ID: 40, Name: "校园", Category: "content", Created: ts, Updated: ts, GalgameCount: 9}},
		},
		Official: []model.GalgameOfficialRelation{
			// list preloads Official.Official WITHOUT its Alias
			{GalgameID: 2, OfficialID: 144, Source: "vndb", Created: ts, Updated: ts,
				Official: &model.GalgameOfficial{ID: 144, Name: "O", Category: "company", Created: ts, Updated: ts, GalgameCount: 42}},
		},
		Cover: []model.GalgameCover{
			{GalgameID: 2, ImageHash: "h0", SortOrder: 0, Source: "vndb", SourceKey: "cv1", Kind: "main", Created: ts},
		},
	}
	model.PopulateEffectiveBanner(g)

	oldJSON, err := json.Marshal(g)
	require.NoError(t, err)
	newJSON, err := json.Marshal(NewGalgameDetail(g))
	require.NoError(t, err)

	assert.JSONEq(t, string(oldJSON), string(newJSON),
		"GalgameDetail must be wire-identical for the lighter list preload subset")
}
