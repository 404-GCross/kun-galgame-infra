// Galgame search OpenAPI (code-first) — SPEC ONLY.
//
// GET /galgame/search is SERVED by the Fiber SearchHandler, which returns raw
// Meilisearch hit maps (map[string]any) on purpose — to pass through Meili's
// _formatted highlight object, field projection (?fields=), and any future
// indexed field without a code change. So this can't be a served Huma op; it's
// spec-only. The hit DATA shape is still single-sourced by EMBEDDING the real
// indexed document (search.GalgameDoc, whose fields are plain JSON types), so if
// the index schema changes the spec follows automatically. The envelope mirrors
// search.GalgameSearchResponse.
package handler

import (
	"context"
	"encoding/json"
	"net/http"

	galgameSearch "api/internal/platform/galgame/search"

	"github.com/danielgtaylor/huma/v2"
)

// galgameSearchHit is one result: the indexed document fields (embedded
// GalgameDoc) plus Meili's optional _formatted highlight variants — a partial
// object of the same string fields with <mark>…</mark> wrapping (present when
// highlight=true, the default). Note: a request with ?fields=… projects a subset
// of the doc fields.
type galgameSearchHit struct {
	galgameSearch.GalgameDoc
	Formatted json.RawMessage `json:"_formatted,omitempty" doc:"Highlighted variants of matched string fields (highlight=true)"`
}

// galgameSearchData mirrors search.GalgameSearchResponse with typed hits.
type galgameSearchData struct {
	Items            []galgameSearchHit        `json:"items"`
	Total            int64                     `json:"total"`
	Facets           map[string]map[string]int `json:"facets,omitempty" doc:"facet → value → count (facets=true)"`
	ProcessingTimeMS int64                     `json:"processing_time_ms"`
	Pending          []galgameSearchHit        `json:"pending,omitempty" doc:"The viewer's own pending/declined matches (include_pending=true + authenticated)"`
}

type galgameSearchInput struct {
	Q                string `query:"q" doc:"Full-text query (localized names / aliases; tag/studio names with include_intro)"`
	Status           string `query:"status" doc:"CSV of statuses; non-admins clamped to {0,2}, default 0 (published)"`
	ContentLimit     string `query:"content_limit" doc:"sfw | nsfw | all (default sfw)"`
	AgeLimit         string `query:"age_limit" doc:"all | r18"`
	OriginalLanguage string `query:"original_language" doc:"CSV; OR filter"`
	TagIDs           string `query:"tag_ids" doc:"CSV; AND filter"`
	OfficialIDs      string `query:"official_ids" doc:"CSV; AND filter"`
	EngineIDs        string `query:"engine_ids" doc:"CSV; AND filter"`
	SeriesID         string `query:"series_id" doc:"Exact series id"`
	ReleasedFrom     string `query:"released_from" doc:"YYYY or YYYY-MM"`
	ReleasedTo       string `query:"released_to" doc:"YYYY or YYYY-MM"`
	ReleasedMonths   string `query:"released_months" doc:"CSV of months 1-12, AND'd on the year range"`
	IncludeIntro     bool   `query:"include_intro" doc:"Also search intro_* / tag / official names"`
	Sort             string `query:"sort" enum:"relevance,released_desc,released_asc,view,updated" doc:"Sort (default relevance)"`
	Page             int    `query:"page" doc:"Page number (default 1)"`
	Limit            int    `query:"limit" doc:"Items per page (default 24, max 100)"`
	Facets           bool   `query:"facets" doc:"Return facet aggregation (default true)"`
	Highlight        bool   `query:"highlight" doc:"Return _formatted highlights (default true)"`
	Fields           string `query:"fields" doc:"CSV attribute projection; empty = all"`
	IncludePending   bool   `query:"include_pending" doc:"Also return the viewer's own pending/declined (needs auth)"`
}

type galgameSearchOutput struct {
	Body calEnvelope[galgameSearchData]
}

// registerGalgameSearchOps adds the search op to the read API (called by
// SetupGalgameReadSpec).
func registerGalgameSearchOps(api huma.API, tags []string) {
	huma.Register(api, huma.Operation{
		OperationID: "searchGalgames", Method: http.MethodGet, Path: "/api/galgame/search",
		Summary: "Full-text galgame search (Meilisearch): filters, facets, highlights, projection", Tags: tags,
	}, func(context.Context, *galgameSearchInput) (*galgameSearchOutput, error) { return &galgameSearchOutput{}, nil })
}
