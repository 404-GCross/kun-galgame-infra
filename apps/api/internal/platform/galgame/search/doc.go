// Package search defines Meilisearch document schemas and implements
// indexer + search service for galgame / tag / official entities.
//
// Design doc: docs/galgame_wiki/05-search-design.md
package search

// Index UIDs (before prefix). Use Client.IndexUID to get the prefixed name.
const (
	IndexGalgames  = "galgames"
	IndexTags      = "galgame_tags"
	IndexOfficials = "galgame_officials"
)

// GalgameDoc is the Meilisearch document for a galgame.
type GalgameDoc struct {
	ID     int    `json:"id"`
	VNDBID string `json:"vndb_id"`
	BID    *int   `json:"bid,omitempty"`

	NameZhCN string   `json:"name_zh_cn"`
	NameJaJP string   `json:"name_ja_jp"`
	NameEnUS string   `json:"name_en_us"`
	NameZhTW string   `json:"name_zh_tw"`
	Aliases  []string `json:"aliases"`

	TagNames      []string `json:"tag_names"`
	TagIDs        []int    `json:"tag_ids"`
	OfficialNames []string `json:"official_names"`
	OfficialIDs   []int    `json:"official_ids"`
	EngineNames   []string `json:"engine_names"`
	EngineIDs     []int    `json:"engine_ids"`
	SeriesID      *int     `json:"series_id,omitempty"`

	// Intro fields are written to the document but excluded from default
	// searchable attributes. They become searchable only when the request
	// opts in via include_intro=true (via attributesToSearchOn).
	IntroZhCN string `json:"intro_zh_cn"`
	IntroJaJP string `json:"intro_ja_jp"`
	IntroEnUS string `json:"intro_en_us"`
	IntroZhTW string `json:"intro_zh_tw"`

	Banner           string `json:"banner"`
	// EffectiveBannerHash is the image_hash of the pinned cover
	// (galgame_cover where sort_order=0) — the same derived field the
	// REST responses expose. Indexed here so list cards rendered from
	// search results can pull from image_service without a second
	// /galgame/:gid round-trip. Empty string when no cover is pinned.
	EffectiveBannerHash string `json:"effective_banner_hash"`
	ContentLimit     string `json:"content_limit"`
	AgeLimit         string `json:"age_limit"`
	OriginalLanguage string `json:"original_language"`
	Status           int    `json:"status"`
	// UserID — owner / submitter. Filterable so the search service can
	// return a user's own pending/declined drafts via include_pending=true.
	UserID           int    `json:"user_id"`
	View             int    `json:"view"`
	// ReleaseDate is the date column rendered as "YYYY-MM-DD" (empty when
	// unknown). ReleaseDateTBA marks "announced, exact date pending".
	// ReleasedYear / ReleasedTS are derived for cheap filter/sort —
	// they stay nil when ReleaseDate is empty.
	ReleaseDate      string `json:"release_date"`
	ReleaseDateTBA   bool   `json:"release_date_tba"`
	ReleasedYear     *int   `json:"released_year,omitempty"`
	ReleasedTS       *int64 `json:"released_ts,omitempty"`
	UpdatedTS        int64  `json:"updated_ts"`
	CreatedTS        int64  `json:"created_ts"`
}

// TagDoc is the Meilisearch document for a galgame tag.
type TagDoc struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Aliases      []string `json:"aliases"`
	Category     string   `json:"category"`
	GalgameCount int      `json:"galgame_count"`
}

// OfficialDoc is the Meilisearch document for a galgame official (developer/publisher).
type OfficialDoc struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Original     string   `json:"original"`
	Aliases      []string `json:"aliases"`
	Category     string   `json:"category"`
	Lang         string   `json:"lang"`
	GalgameCount int      `json:"galgame_count"`
}
