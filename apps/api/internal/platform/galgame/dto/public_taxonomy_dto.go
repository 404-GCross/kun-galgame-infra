package dto

// NextMoe open-API public taxonomy projection (W1b). Curated by-id faces for the
// four taxonomy families (tags / officials / engines / series) — the frozen v1
// public contract, decoupled from the internal bridge's raw-model shapes. The
// bridge legacy quirks (bare-array engine list, /:name + query-param-id entity
// lookups, raw GORM rows) deliberately do NOT carry over: entity access is
// by-id only, list envelopes are uniformly {items, total} with page/limit
// pagination, and every record is a curated struct (the consumed-union field
// vocabulary, no wiki-internal columns).

// PublicTagEntity is one tag's curated public record — the shape of both a list
// item (GET /v1/galgame/tags) and the by-id detail (GET /v1/galgame/tags/{id}).
// aliases is always present ([] when none); created / updated are UTC RFC3339
// ("" on the zero value). galgame_count is the published (status=0) galgame
// count, content_limit-independent (mirrors the bridge tag-count semantics).
type PublicTagEntity struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	Description  string   `json:"description"`
	Aliases      []string `json:"aliases"`
	GalgameCount int      `json:"galgame_count"`
	Created      string   `json:"created"`
	Updated      string   `json:"updated"`
}

// PublicTagListData is the tag list envelope (GET /v1/galgame/tags): a page of
// curated tag records + the filtered total. Page/limit pagination.
type PublicTagListData struct {
	Items []PublicTagEntity `json:"items"`
	Total int64             `json:"total"`
}

// PublicOfficialEntity is one maker's curated public record — the shape of both a
// list item (GET /v1/galgame/officials) and the by-id detail
// (GET /v1/galgame/officials/{id}). Carries the maker's self-description
// (original, link, lang, description) + aliases + published galgame_count.
type PublicOfficialEntity struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	Original     string   `json:"original"`
	Link         string   `json:"link"`
	Lang         string   `json:"lang"`
	Description  string   `json:"description"`
	Aliases      []string `json:"aliases"`
	GalgameCount int      `json:"galgame_count"`
}

// PublicOfficialListData is the maker list envelope (GET /v1/galgame/officials).
type PublicOfficialListData struct {
	Items []PublicOfficialEntity `json:"items"`
	Total int64                  `json:"total"`
}

// PublicEngineEntity is one engine's curated public record — the shape of both a
// list item (GET /v1/galgame/engines) and the by-id detail
// (GET /v1/galgame/engines/{id}). alias is the engine's alias set (always
// present, [] when none); galgame_count is the published galgame count.
type PublicEngineEntity struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Alias        []string `json:"alias"`
	GalgameCount int      `json:"galgame_count"`
}

// PublicEngineListData is the engine list envelope (GET /v1/galgame/engines) —
// page/limit paginated (the bridge's bare-array engine list does not carry over).
type PublicEngineListData struct {
	Items []PublicEngineEntity `json:"items"`
	Total int64                `json:"total"`
}

// PublicSeriesEntity is one series' curated public record — the shape of both a
// list item (GET /v1/galgame/series) and the by-id detail
// (GET /v1/galgame/series/{id}). galgames is the member preview projected to the
// thin /v1 PublicGalgameItem (content_limit-gated, NSFW pins dropped on the sfw
// face); the list caps it to a handful of members while the detail returns the
// full gated membership. galgame_count is content_limit-filtered (mirrors the
// bridge series-count semantics: the count stays in sync with the gated preview).
type PublicSeriesEntity struct {
	ID           int                 `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	GalgameCount int                 `json:"galgame_count"`
	Galgames     []PublicGalgameItem `json:"galgames"`
}

// PublicSeriesListData is the series list envelope (GET /v1/galgame/series).
type PublicSeriesListData struct {
	Items []PublicSeriesEntity `json:"items"`
	Total int64                `json:"total"`
}

// PublicItemListData is the thin-item envelope for the tag-intersection face
// (GET /v1/galgame/tags/multi): the published galgames carrying ALL requested
// tag ids, projected to thin /v1 items + the filtered total. Page/limit.
type PublicItemListData struct {
	Items []PublicGalgameItem `json:"items"`
	Total int64               `json:"total"`
}

// PublicIDsData is the {ids:[]int} envelope for the galgame-ids reverse faces
// (tags / officials / engines /{id}/galgame-ids) — the published galgame ids
// under an entity, for the forum's local set-intersection consumer. ids is
// always present ([] when the entity carries no published galgame).
type PublicIDsData struct {
	IDs []int `json:"ids"`
}

// PublicTagSearchItem is one Meilisearch tag-search hit: the tag id + name +
// aliases + category + published galgame_count. The curated projection of the
// tag search index (mirrors the bridge Meili-backed /tag/search).
type PublicTagSearchItem struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Aliases      []string `json:"aliases"`
	Category     string   `json:"category"`
	GalgameCount int      `json:"galgame_count"`
}

// PublicTagSearchData is the tag search envelope (GET /v1/galgame/tags/search):
// relevance-ordered hits + the total + the Meilisearch processing time.
type PublicTagSearchData struct {
	Items            []PublicTagSearchItem `json:"items"`
	Total            int64                 `json:"total"`
	ProcessingTimeMS int64                 `json:"processing_time_ms"`
}

// PublicOfficialSearchItem is one Meilisearch maker-search hit: the maker id +
// name + aliases + category + published galgame_count + original + lang.
type PublicOfficialSearchItem struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Aliases      []string `json:"aliases"`
	Category     string   `json:"category"`
	GalgameCount int      `json:"galgame_count"`
	Original     string   `json:"original"`
	Lang         string   `json:"lang"`
}

// PublicOfficialSearchData is the maker search envelope
// (GET /v1/galgame/officials/search).
type PublicOfficialSearchData struct {
	Items            []PublicOfficialSearchItem `json:"items"`
	Total            int64                      `json:"total"`
	ProcessingTimeMS int64                      `json:"processing_time_ms"`
}
