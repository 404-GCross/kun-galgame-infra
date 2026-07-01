package dto

import (
	"encoding/json"

	"api/internal/platform/galgame/model"
)

// GalgameDetail is the typed mirror of the full galgame graph returned by
// GET /galgame/:gid (the `galgame` field; `users` is a separate map — see the
// handler). It faithfully reproduces the JSON of a FindByID-preloaded
// model.Galgame (Alias / Series / Contributor / Link / Tag.Tag /
// Official.Official.Alias / Engine.Engine / Cover / Screenshot, covers +
// screenshots dims-enriched, EffectiveBannerHash populated). A wire-equality
// test (galgame_detail_dto_test.go) pins it to model.Galgame's marshaling, so
// the Huma spec / generated TS can't drift and the handler swap is a
// non-behaviour change. PR / History are NOT preloaded by the detail path, so
// they are intentionally absent here (as in the current wire).

// DetailScreenshot mirrors a dims-enriched galgame_screenshot row.
type DetailScreenshot struct {
	GalgameID int     `json:"galgame_id"`
	ImageHash string  `json:"image_hash"`
	SortOrder int     `json:"sort_order"`
	Caption   string  `json:"caption"`
	Sexual    int16   `json:"sexual"`
	Violence  int16   `json:"violence"`
	Source    string  `json:"source"`
	SourceKey string  `json:"source_key"`
	Created   *string `json:"created"`
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
	Thumbhash string  `json:"thumbhash,omitempty"`
}

// DetailTag mirrors the nested tag (Tag.Tag — its own alias is NOT preloaded).
type DetailTag struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Category     string  `json:"category"`
	Description  string  `json:"description"`
	Created      *string `json:"created"`
	Updated      *string `json:"updated"`
	GalgameCount int     `json:"galgame_count"`
}

// DetailTagRelation mirrors one galgame↔tag relation row.
type DetailTagRelation struct {
	GalgameID    int        `json:"galgame_id"`
	TagID        int        `json:"tag_id"`
	SpoilerLevel int        `json:"spoiler_level"`
	Source       string     `json:"source"`
	Created      *string    `json:"created"`
	Updated      *string    `json:"updated"`
	Tag          *DetailTag `json:"tag,omitempty"`
}

// DetailOfficialAlias mirrors a preloaded official alias row.
type DetailOfficialAlias struct {
	ID                int     `json:"id"`
	Name              string  `json:"name"`
	GalgameOfficialID int     `json:"galgame_official_id"`
	Created           *string `json:"created"`
	Updated           *string `json:"updated"`
}

// DetailOfficial mirrors the nested official (Official.Official + its Alias).
type DetailOfficial struct {
	ID           int                   `json:"id"`
	Name         string                `json:"name"`
	Original     string                `json:"original"`
	Link         string                `json:"link"`
	Category     string                `json:"category"`
	Lang         string                `json:"lang"`
	Description  string                `json:"description"`
	Created      *string               `json:"created"`
	Updated      *string               `json:"updated"`
	GalgameCount int                   `json:"galgame_count"`
	Alias        []DetailOfficialAlias `json:"alias,omitempty"`
}

// DetailOfficialRelation mirrors one galgame↔official relation row.
type DetailOfficialRelation struct {
	GalgameID  int             `json:"galgame_id"`
	OfficialID int             `json:"official_id"`
	Source     string          `json:"source"`
	Created    *string         `json:"created"`
	Updated    *string         `json:"updated"`
	Official   *DetailOfficial `json:"official,omitempty"`
}

// DetailEngine mirrors the nested engine (Engine.Engine). Alias is a jsonb array
// carried verbatim.
type DetailEngine struct {
	ID           int             `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Alias        json.RawMessage `json:"alias"`
	Created      *string         `json:"created"`
	Updated      *string         `json:"updated"`
	GalgameCount int             `json:"galgame_count"`
}

// DetailEngineRelation mirrors one galgame↔engine relation row.
type DetailEngineRelation struct {
	GalgameID int           `json:"galgame_id"`
	EngineID  int           `json:"engine_id"`
	Created   *string       `json:"created"`
	Updated   *string       `json:"updated"`
	Engine    *DetailEngine `json:"engine,omitempty"`
}

// DetailLink mirrors a galgame_link row.
type DetailLink struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Link      string  `json:"link"`
	Source    string  `json:"source"`
	SourceKey string  `json:"source_key"`
	GalgameID int     `json:"galgame_id"`
	UserID    int     `json:"user_id"`
	Created   *string `json:"created"`
	Updated   *string `json:"updated"`
}

// DetailAlias mirrors a galgame_alias row.
type DetailAlias struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	GalgameID int     `json:"galgame_id"`
	Created   *string `json:"created"`
	Updated   *string `json:"updated"`
}

// DetailContributor mirrors a galgame_contributor row.
type DetailContributor struct {
	ID        int     `json:"id"`
	GalgameID int     `json:"galgame_id"`
	UserID    int     `json:"user_id"`
	Created   *string `json:"created"`
	Updated   *string `json:"updated"`
}

// DetailSeries mirrors the nested series (its `galgame` list is not loaded).
type DetailSeries struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Created      *string `json:"created"`
	Updated      *string `json:"updated"`
	GalgameCount int     `json:"galgame_count"`
}

// GalgameDetail is the full detail graph.
type GalgameDetail struct {
	ID                  int      `json:"id"`
	VNDBID              string   `json:"vndb_id"`
	BangumiID           *int     `json:"bid,omitempty"`
	ReleaseDate         *string  `json:"release_date"`
	ReleaseDateTBA      bool     `json:"release_date_tba"`
	ReleasePrecision    string   `json:"release_precision"`
	NameEnUS            string   `json:"name_en_us"`
	NameJaJP            string   `json:"name_ja_jp"`
	NameZhCN            string   `json:"name_zh_cn"`
	NameZhTW            string   `json:"name_zh_tw"`
	Banner              string   `json:"banner"`
	IntroEnUS           string   `json:"intro_en_us"`
	IntroJaJP           string   `json:"intro_ja_jp"`
	IntroZhCN           string   `json:"intro_zh_cn"`
	IntroZhTW           string   `json:"intro_zh_tw"`
	ContentLimit        string   `json:"content_limit"`
	Status              int      `json:"status"`
	View                int      `json:"view"`
	ResourceUpdateTime  *string  `json:"resource_update_time"`
	OriginalLanguage    string   `json:"original_language"`
	AgeLimit            string   `json:"age_limit"`
	UserID              int      `json:"user_id"`
	SeriesID            *int     `json:"series_id"`
	Created             *string  `json:"created"`
	Updated             *string  `json:"updated"`
	EffectiveBannerHash *string  `json:"effective_banner_hash,omitempty"`

	Series      *DetailSeries            `json:"series,omitempty"`
	Alias       []DetailAlias            `json:"alias,omitempty"`
	Link        []DetailLink             `json:"link,omitempty"`
	Contributor []DetailContributor      `json:"contributor,omitempty"`
	Official    []DetailOfficialRelation `json:"official,omitempty"`
	Engine      []DetailEngineRelation   `json:"engine,omitempty"`
	Tag         []DetailTagRelation      `json:"tag,omitempty"`
	Cover       []CalendarCover          `json:"covers,omitempty"`
	Screenshot  []DetailScreenshot       `json:"screenshots,omitempty"`
}

// NewGalgameDetail maps a FindByID-preloaded model.Galgame to the typed detail.
func NewGalgameDetail(g *model.Galgame) GalgameDetail {
	d := GalgameDetail{
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

	if g.Series != nil {
		s := g.Series
		d.Series = &DetailSeries{
			ID: s.ID, Name: s.Name, Description: s.Description,
			Created: fmtTS(s.Created), Updated: fmtTS(s.Updated), GalgameCount: s.GalgameCount,
		}
	}
	for i := range g.Alias {
		a := &g.Alias[i]
		d.Alias = append(d.Alias, DetailAlias{
			ID: a.ID, Name: a.Name, GalgameID: a.GalgameID,
			Created: fmtTS(a.Created), Updated: fmtTS(a.Updated),
		})
	}
	for i := range g.Link {
		l := &g.Link[i]
		d.Link = append(d.Link, DetailLink{
			ID: l.ID, Name: l.Name, Link: l.Link, Source: l.Source, SourceKey: l.SourceKey,
			GalgameID: l.GalgameID, UserID: l.UserID, Created: fmtTS(l.Created), Updated: fmtTS(l.Updated),
		})
	}
	for i := range g.Contributor {
		c := &g.Contributor[i]
		d.Contributor = append(d.Contributor, DetailContributor{
			ID: c.ID, GalgameID: c.GalgameID, UserID: c.UserID,
			Created: fmtTS(c.Created), Updated: fmtTS(c.Updated),
		})
	}
	for i := range g.Official {
		r := &g.Official[i]
		rel := DetailOfficialRelation{
			GalgameID: r.GalgameID, OfficialID: r.OfficialID, Source: r.Source,
			Created: fmtTS(r.Created), Updated: fmtTS(r.Updated),
		}
		if r.Official != nil {
			o := r.Official
			do := &DetailOfficial{
				ID: o.ID, Name: o.Name, Original: o.Original, Link: o.Link,
				Category: o.Category, Lang: o.Lang, Description: o.Description,
				Created: fmtTS(o.Created), Updated: fmtTS(o.Updated), GalgameCount: o.GalgameCount,
			}
			for j := range o.Alias {
				oa := &o.Alias[j]
				do.Alias = append(do.Alias, DetailOfficialAlias{
					ID: oa.ID, Name: oa.Name, GalgameOfficialID: oa.GalgameOfficialID,
					Created: fmtTS(oa.Created), Updated: fmtTS(oa.Updated),
				})
			}
			rel.Official = do
		}
		d.Official = append(d.Official, rel)
	}
	for i := range g.Engine {
		r := &g.Engine[i]
		rel := DetailEngineRelation{
			GalgameID: r.GalgameID, EngineID: r.EngineID,
			Created: fmtTS(r.Created), Updated: fmtTS(r.Updated),
		}
		if r.Engine != nil {
			e := r.Engine
			rel.Engine = &DetailEngine{
				ID: e.ID, Name: e.Name, Description: e.Description,
				Alias:   json.RawMessage(e.Alias),
				Created: fmtTS(e.Created), Updated: fmtTS(e.Updated), GalgameCount: e.GalgameCount,
			}
		}
		d.Engine = append(d.Engine, rel)
	}
	for i := range g.Tag {
		r := &g.Tag[i]
		rel := DetailTagRelation{
			GalgameID: r.GalgameID, TagID: r.TagID, SpoilerLevel: r.SpoilerLevel, Source: r.Source,
			Created: fmtTS(r.Created), Updated: fmtTS(r.Updated),
		}
		if r.Tag != nil {
			t := r.Tag
			rel.Tag = &DetailTag{
				ID: t.ID, Name: t.Name, Category: t.Category, Description: t.Description,
				Created: fmtTS(t.Created), Updated: fmtTS(t.Updated), GalgameCount: t.GalgameCount,
			}
		}
		d.Tag = append(d.Tag, rel)
	}
	for i := range g.Cover {
		c := &g.Cover[i]
		d.Cover = append(d.Cover, CalendarCover{
			GalgameID: c.GalgameID, ImageHash: c.ImageHash, SortOrder: c.SortOrder,
			Sexual: c.Sexual, Violence: c.Violence, Source: c.Source, SourceKey: c.SourceKey,
			Kind: c.Kind, Created: fmtTS(c.Created), Width: c.Width, Height: c.Height, Thumbhash: c.Thumbhash,
		})
	}
	for i := range g.Screenshot {
		s := &g.Screenshot[i]
		d.Screenshot = append(d.Screenshot, DetailScreenshot{
			GalgameID: s.GalgameID, ImageHash: s.ImageHash, SortOrder: s.SortOrder, Caption: s.Caption,
			Sexual: s.Sexual, Violence: s.Violence, Source: s.Source, SourceKey: s.SourceKey,
			Created: fmtTS(s.Created), Width: s.Width, Height: s.Height, Thumbhash: s.Thumbhash,
		})
	}
	return d
}

// NewGalgameDetails maps a slice of galgames to detail DTOs. Used by GET /galgame
// (list) too: its items are a lighter preload subset (Tag.Tag / Official.Official
// / Cover), and GalgameDetail's relations are all omitempty, so the un-preloaded
// ones are simply absent — the same shape the raw model already marshals to.
func NewGalgameDetails(gs []model.Galgame) []GalgameDetail {
	out := make([]GalgameDetail, 0, len(gs))
	for i := range gs {
		out = append(out, NewGalgameDetail(&gs[i]))
	}
	return out
}

// GalgameListData is the `data` payload of GET /galgame (list): items + total.
type GalgameListData struct {
	Items []GalgameDetail `json:"items"`
	Total int64           `json:"total"`
}

// UserGalgameListData is the `data` payload of the profile galgame lists
// (GET /galgame/user/:id/galgames and /contributed): briefs keyed as `galgames`.
type UserGalgameListData struct {
	Galgames []GalgameBrief `json:"galgames"`
	Total    int64          `json:"total"`
}

// GalgameContributorWithUser is one row of GET /galgame/:gid/contributors — the
// contributor plus the resolved user brief. DetailContributor is pinned to
// model.GalgameContributor by the detail wire-equality test, so this stays
// faithful to the handler's inline shape.
type GalgameContributorWithUser struct {
	DetailContributor
	User *UserBrief `json:"user,omitempty"`
}

// CheckVNDBResult is the body of GET /galgame/check: whether a vndb_id already
// exists and (if so) which galgame id holds it.
type CheckVNDBResult struct {
	Exists    bool `json:"exists"`
	GalgameID *int `json:"galgame_id,omitempty"`
}

// MineGalgameListData is the body of GET /galgame/mine: the viewer's own
// submissions (all statuses) as light MineGalgame rows.
type MineGalgameListData struct {
	Items []MineGalgame `json:"items"`
	Total int64         `json:"total"`
}

// MessageMineData is the body of GET /galgame/messages/mine: the viewer's own
// galgame notifications. (MessageResponse.CreatedAt is a model.Timestamp, now
// typed by its huma.SchemaProvider as a date-time string.)
type MessageMineData struct {
	Items []MessageResponse `json:"items"`
	Total int64             `json:"total"`
}
