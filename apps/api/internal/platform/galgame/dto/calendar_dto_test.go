package dto

import (
	"encoding/json"
	"testing"
	"time"

	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCalendarItem_WireIdenticalToModel pins CalendarItem to the JSON the
// calendar endpoints already emit: a calendar-preloaded model.Galgame (Cover +
// Official.Official loaded, EffectiveBannerHash populated) must marshal to the
// SAME JSON (semantically) as NewCalendarItem(g). This makes the Huma migration
// a non-behaviour-change and guards against a missed/renamed field.
func TestCalendarItem_WireIdenticalToModel(t *testing.T) {
	ts := model.Timestamp(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	rd := model.Date(time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC))
	bid := 555
	sid := 7
	g := &model.Galgame{
		ID: 1, VNDBID: "v54934", BangumiID: &bid,
		ReleaseDate: &rd, ReleaseDateTBA: false, ReleasePrecision: "day",
		NameEnUS: "Name", NameJaJP: "名前", NameZhCN: "名字", NameZhTW: "名字",
		Banner:       "https://t.vndb.org/cv/00/1.jpg",
		IntroEnUS:    "intro-en", IntroJaJP: "intro-ja", IntroZhCN: "intro-zh", IntroZhTW: "intro-tw",
		ContentLimit: "sfw", Status: 2, View: 100,
		ResourceUpdateTime: ts, OriginalLanguage: "ja-jp", AgeLimit: "r18",
		UserID: 1, SeriesID: &sid, Created: ts, Updated: ts,
		Cover: []model.GalgameCover{
			{GalgameID: 1, ImageHash: "hash0", SortOrder: 0, Source: "vndb", SourceKey: "cv1", Kind: "main", Created: ts},
			{GalgameID: 1, ImageHash: "hash1", SortOrder: 1, Source: "vndb", SourceKey: "cv2", Kind: "pkgfront", Created: ts},
		},
		Official: []model.GalgameOfficialRelation{
			{
				GalgameID: 1, OfficialID: 144, Source: "vndb", Created: ts, Updated: ts,
				Official: &model.GalgameOfficial{
					ID: 144, Name: "オトメイト", Original: "Otomate", Link: "https://otomate.jp",
					Category: "company", Lang: "ja", Created: ts, Updated: ts, GalgameCount: 42,
				},
			},
		},
	}
	model.PopulateEffectiveBanner(g)

	oldJSON, err := json.Marshal(g)
	require.NoError(t, err)
	newJSON, err := json.Marshal(NewCalendarItem(g))
	require.NoError(t, err)

	assert.JSONEq(t, string(oldJSON), string(newJSON),
		"CalendarItem must be wire-identical to the calendar-preloaded model.Galgame")
}
