package dto

import (
	"time"

	"api/internal/platform/galgame/model"
)

// Calendar response DTOs — the code-first source for the release-calendar
// OpenAPI spec (cmd/gen-openapi -galgame-calendar) and the frontend's generated
// TypeScript. They are a FAITHFUL, typed mirror of the JSON the calendar
// endpoints already emit (model.Galgame with Cover + Official.Official preloaded
// and EffectiveBannerHash populated) — a wire-equality test
// (calendar_dto_test.go) pins them to model.Galgame's marshaling so this is not
// a behaviour change. Custom time types are rendered exactly as the model's
// MarshalJSON does: Date → "YYYY-MM-DD" (UTC) or null; Timestamp → UTC RFC3339
// (second precision) or null.

// mirror model/date.go + model/timestamp.go so the wire format is identical.
const (
	calDateLayout = "2006-01-02"
	calTSLayout   = "2006-01-02T15:04:05Z"
)

func fmtTS(ts model.Timestamp) *string {
	t := time.Time(ts)
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(calTSLayout)
	return &s
}

func fmtDate(d *model.Date) *string {
	if d == nil {
		return nil
	}
	t := time.Time(*d)
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(calDateLayout)
	return &s
}

// CalendarCover mirrors a preloaded galgame_cover row (width/height/thumbhash are
// read-time image_service enrichment — absent on the calendar path, omitempty).
type CalendarCover struct {
	GalgameID int     `json:"galgame_id"`
	ImageHash string  `json:"image_hash"`
	SortOrder int     `json:"sort_order"`
	Sexual    int16   `json:"sexual"`
	Violence  int16   `json:"violence"`
	Source    string  `json:"source"`
	SourceKey string  `json:"source_key"`
	Kind      string  `json:"kind"`
	Created   *string `json:"created"`
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
	Thumbhash string  `json:"thumbhash,omitempty"`
}

// CalendarOfficial mirrors the nested developer/publisher (Official.Official).
type CalendarOfficial struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Original     string  `json:"original"`
	Link         string  `json:"link"`
	Category     string  `json:"category"`
	Lang         string  `json:"lang"`
	Description  string  `json:"description"`
	Created      *string `json:"created"`
	Updated      *string `json:"updated"`
	GalgameCount int     `json:"galgame_count"`
}

// CalendarOfficialRelation mirrors one galgame↔official relation row.
type CalendarOfficialRelation struct {
	GalgameID  int               `json:"galgame_id"`
	OfficialID int               `json:"official_id"`
	Source     string            `json:"source"`
	Created    *string           `json:"created"`
	Updated    *string           `json:"updated"`
	Official   *CalendarOfficial `json:"official,omitempty"`
}

// CalendarItem is one release-calendar entry — the typed mirror of the
// calendar-preloaded model.Galgame JSON.
type CalendarItem struct {
	ID                  int                        `json:"id"`
	VNDBID              string                     `json:"vndb_id"`
	BangumiID           *int                       `json:"bid,omitempty"`
	ReleaseDate         *string                    `json:"release_date"`
	ReleaseDateTBA      bool                       `json:"release_date_tba"`
	ReleasePrecision    string                     `json:"release_precision"`
	NameEnUS            string                     `json:"name_en_us"`
	NameJaJP            string                     `json:"name_ja_jp"`
	NameZhCN            string                     `json:"name_zh_cn"`
	NameZhTW            string                     `json:"name_zh_tw"`
	Banner              string                     `json:"banner"`
	IntroEnUS           string                     `json:"intro_en_us"`
	IntroJaJP           string                     `json:"intro_ja_jp"`
	IntroZhCN           string                     `json:"intro_zh_cn"`
	IntroZhTW           string                     `json:"intro_zh_tw"`
	ContentLimit        string                     `json:"content_limit"`
	Status              int                        `json:"status"`
	View                int                        `json:"view"`
	ResourceUpdateTime  *string                    `json:"resource_update_time"`
	OriginalLanguage    string                     `json:"original_language"`
	AgeLimit            string                     `json:"age_limit"`
	UserID              int                        `json:"user_id"`
	SeriesID            *int                       `json:"series_id"`
	Created             *string                    `json:"created"`
	Updated             *string                    `json:"updated"`
	EffectiveBannerHash *string                    `json:"effective_banner_hash,omitempty"`
	Cover               []CalendarCover            `json:"covers,omitempty"`
	Official            []CalendarOfficialRelation `json:"official,omitempty"`
}

// NewCalendarItem maps a calendar-preloaded model.Galgame to the typed item.
func NewCalendarItem(g *model.Galgame) CalendarItem {
	it := CalendarItem{
		ID:                  g.ID,
		VNDBID:              g.VNDBID,
		BangumiID:           g.BangumiID,
		ReleaseDate:         fmtDate(g.ReleaseDate),
		ReleaseDateTBA:      g.ReleaseDateTBA,
		ReleasePrecision:    g.ReleasePrecision,
		NameEnUS:            g.NameEnUS,
		NameJaJP:            g.NameJaJP,
		NameZhCN:            g.NameZhCN,
		NameZhTW:            g.NameZhTW,
		Banner:              g.Banner,
		IntroEnUS:           g.IntroEnUS,
		IntroJaJP:           g.IntroJaJP,
		IntroZhCN:           g.IntroZhCN,
		IntroZhTW:           g.IntroZhTW,
		ContentLimit:        g.ContentLimit,
		Status:              g.Status,
		View:                g.View,
		ResourceUpdateTime:  fmtTS(g.ResourceUpdateTime),
		OriginalLanguage:    g.OriginalLanguage,
		AgeLimit:            g.AgeLimit,
		UserID:              g.UserID,
		SeriesID:            g.SeriesID,
		Created:             fmtTS(g.Created),
		Updated:             fmtTS(g.Updated),
		EffectiveBannerHash: g.EffectiveBannerHash,
	}
	for i := range g.Cover {
		c := &g.Cover[i]
		it.Cover = append(it.Cover, CalendarCover{
			GalgameID: c.GalgameID, ImageHash: c.ImageHash, SortOrder: c.SortOrder,
			Sexual: c.Sexual, Violence: c.Violence, Source: c.Source, SourceKey: c.SourceKey,
			Kind: c.Kind, Created: fmtTS(c.Created), Width: c.Width, Height: c.Height, Thumbhash: c.Thumbhash,
		})
	}
	for i := range g.Official {
		r := &g.Official[i]
		cr := CalendarOfficialRelation{
			GalgameID: r.GalgameID, OfficialID: r.OfficialID, Source: r.Source,
			Created: fmtTS(r.Created), Updated: fmtTS(r.Updated),
		}
		if r.Official != nil {
			o := r.Official
			cr.Official = &CalendarOfficial{
				ID: o.ID, Name: o.Name, Original: o.Original, Link: o.Link,
				Category: o.Category, Lang: o.Lang, Description: o.Description,
				Created: fmtTS(o.Created), Updated: fmtTS(o.Updated), GalgameCount: o.GalgameCount,
			}
		}
		it.Official = append(it.Official, cr)
	}
	return it
}

// NewCalendarItems maps a slice of calendar-preloaded galgames.
func NewCalendarItems(gs []model.Galgame) []CalendarItem {
	out := make([]CalendarItem, 0, len(gs))
	for i := range gs {
		out = append(out, NewCalendarItem(&gs[i]))
	}
	return out
}

// ── Calendar response envelopes (the `data` of the house response) ──
// These are the single source for both the Fiber handler's response body AND the
// Huma-derived OpenAPI spec, so the two can't drift.

// CalendarLinks are the self/prev/next navigation URLs for the month view.
type CalendarLinks struct {
	Self string `json:"self"`
	Prev string `json:"prev"`
	Next string `json:"next"`
}

// CalendarMonthMeta is the month view's navigation + count metadata.
type CalendarMonthMeta struct {
	PrevMonth string `json:"prev_month"`
	NextMonth string `json:"next_month"`
	HasPrev   bool   `json:"has_prev"`
	HasNext   bool   `json:"has_next"`
	MinMonth  string `json:"min_month"`
	MaxMonth  string `json:"max_month"`
	Count     int64  `json:"count"`
}

// CalendarCountMeta is the count-only metadata for the year-pending / TBA buckets.
type CalendarCountMeta struct {
	Count int64 `json:"count"`
}

// CalendarMonthData is the `data` payload of GET /galgame/calendar.
type CalendarMonthData struct {
	Month string            `json:"month"`
	Today string            `json:"today"`
	Items []CalendarItem    `json:"items"`
	Links CalendarLinks     `json:"links"`
	Meta  CalendarMonthMeta `json:"meta"`
}

// CalendarPendingData is the `data` payload of GET /galgame/calendar/pending.
type CalendarPendingData struct {
	Year  string            `json:"year"`
	Items []CalendarItem    `json:"items"`
	Meta  CalendarCountMeta `json:"meta"`
}

// CalendarTBAData is the `data` payload of GET /galgame/calendar/tba.
type CalendarTBAData struct {
	Items []CalendarItem    `json:"items"`
	Meta  CalendarCountMeta `json:"meta"`
}
