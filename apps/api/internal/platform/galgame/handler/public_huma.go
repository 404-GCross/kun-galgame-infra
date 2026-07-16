// NextMoe open-API galgame public projection (/v1/galgame/*) — SPEC ONLY.
//
// Like galgame_read_huma.go / calendar_huma.go, these operations are SERVED by
// the Fiber handlers in public_handler.go (+ the calendar / reverse-lookup
// passthroughs); this file registers them on a Huma API purely so
// cmd/gen-openapi -galgame-public can derive an INDEPENDENT public spec from the
// same dto.Public* types the handlers return — the frozen v1 contract, decoupled
// from the internal read spec. Never mounted on the live service.
//
// Scope: the five aggregate endpoints (list / detail / batch / search / changes)
// are modeled with the frozen public DTOs; the calendar + entity→galgames
// reverse-lookups are whitelisted passthroughs modeled with their existing
// (already-published) DTOs. The bare taxonomy list endpoints
// (/tags,/officials,/engines,/series) are served but intentionally omitted here
// — they return the internal model shape, which is not a frozen public contract
// (a curated public projection for them is a later step).
package handler

import (
	"context"
	"net/http"

	"api/internal/platform/galgame/dto"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// ─────────────── input types (query/path params for the public spec) ───────────────
//
// The step-07 query-flexibility params (include=officials,scores + fields=) are
// optional and add-only over the frozen step-02 contract: omitting them yields
// the byte-identical default response (the oasdiff gate enforces this).

type publicListInput struct {
	Sort         string `query:"sort" enum:"id,release_date" doc:"Sort key: id (default, ascending) or release_date (newest first, undated last)"`
	Cursor       string `query:"cursor" doc:"Opaque keyset cursor from a prior response's next_cursor; omit for the first page"`
	Limit        int    `query:"limit" doc:"Items per page 1-100 (default 20)"`
	Include      string `query:"include" doc:"Comma-separated blocks to expand on each item: officials,scores (default: none). Unknown names are ignored."`
	Fields       string `query:"fields" doc:"Comma-separated top-level response keys to return (sparse fieldset); id is always included and unknown names are ignored (never a 400). Write keys in alphabetical order to maximize CDN cache hits."`
	ContentLimit string `query:"content_limit" doc:"Reserved; Phase 1 is always sfw"`
}
type publicListOutput struct {
	Body publicEnvelope[dto.PublicListData]
}

type publicDetailInput struct {
	ID           int    `path:"id" doc:"Galgame ID"`
	Include      string `query:"include" doc:"Comma-separated heavy blocks to include: intro,scores,covers,taxonomy (default: none)"`
	Fields       string `query:"fields" doc:"Comma-separated top-level response keys to return (sparse fieldset); id is always included and unknown names are ignored (never a 400). Write keys in alphabetical order to maximize CDN cache hits."`
	ContentLimit string `query:"content_limit" doc:"Reserved; Phase 1 is always sfw"`
}
type publicDetailOutput struct {
	Body publicEnvelope[dto.PublicGalgame]
}

type publicBatchInput struct {
	IDs          string `query:"ids" doc:"Comma-separated galgame IDs (1-100)"`
	View         string `query:"view" enum:"brief,detail" doc:"brief (default) = thin items; detail = full aggregate records (no include)"`
	Include      string `query:"include" doc:"Comma-separated blocks to expand on each item (brief view only): officials,scores (default: none). Unknown names are ignored."`
	Fields       string `query:"fields" doc:"Comma-separated top-level response keys to return (sparse fieldset); id is always included and unknown names are ignored (never a 400). Write keys in alphabetical order to maximize CDN cache hits."`
	ContentLimit string `query:"content_limit" doc:"Reserved; Phase 1 is always sfw"`
}
type publicBatchOutput struct {
	Body publicEnvelope[dto.PublicBatchData]
}

type publicSearchInput struct {
	Q                string `query:"q" doc:"Free-text query across localized names + aliases"`
	Sort             string `query:"sort" doc:"relevance (default) / released_desc / released_asc / view / updated"`
	Page             int    `query:"page" doc:"Page number (default 1)"`
	Limit            int    `query:"limit" doc:"Items per page (default 24)"`
	Include          string `query:"include" doc:"Comma-separated blocks to expand on each item: officials,scores (default: none). Unknown names are ignored."`
	Fields           string `query:"fields" doc:"Comma-separated top-level response keys to return (sparse fieldset); id is always included and unknown names are ignored (never a 400). Write keys in alphabetical order to maximize CDN cache hits."`
	AgeLimit         string `query:"age_limit" doc:"all | r18"`
	OriginalLanguage string `query:"original_language" doc:"CSV of BCP-47 language tags"`
	TagIDs           string `query:"tag_ids" doc:"CSV of tag ids (AND)"`
	OfficialIDs      string `query:"official_ids" doc:"CSV of maker ids (AND)"`
	EngineIDs        string `query:"engine_ids" doc:"CSV of engine ids (AND)"`
	SeriesID         int    `query:"series_id" doc:"Filter by series id"`
	ReleasedFrom     string `query:"released_from" doc:"Release lower bound, YYYY or YYYY-MM"`
	ReleasedTo       string `query:"released_to" doc:"Release upper bound, YYYY or YYYY-MM"`
	ReleasedMonths   string `query:"released_months" doc:"CSV of months 1-12"`
}
type publicSearchOutput struct {
	Body publicEnvelope[dto.PublicSearchData]
}

type publicChangesInput struct {
	Cursor       string `query:"cursor" doc:"Opaque keyset cursor from a prior response's next_cursor; omit for the first page"`
	Limit        int    `query:"limit" doc:"Items per page 1-500 (default 100)"`
	ContentLimit string `query:"content_limit" doc:"Reserved; Phase 1 is always sfw"`
}
type publicChangesOutput struct {
	Body publicEnvelope[dto.PublicChangesData]
}

// passthrough inputs/outputs (whitelisted; existing DTOs).

type publicCalendarInput struct {
	Month            string `query:"month" doc:"ISO month YYYY-MM (default: current JST month)"`
	OriginalLanguage string `query:"original_language" doc:"CSV of BCP-47 tags, or 'all'"`
}
type publicCalendarOutput struct {
	Body publicEnvelope[dto.CalendarMonthData]
}
type publicCalendarPendingInput struct {
	Year             string `query:"year" doc:"Year YYYY (default: current JST year)"`
	OriginalLanguage string `query:"original_language" doc:"CSV of BCP-47 tags, or 'all'"`
}
type publicCalendarPendingOutput struct {
	Body publicEnvelope[dto.CalendarPendingData]
}
type publicCalendarTBAInput struct {
	OriginalLanguage string `query:"original_language" doc:"CSV of BCP-47 tags, or 'all'"`
}
type publicCalendarTBAOutput struct {
	Body publicEnvelope[dto.CalendarTBAData]
}

type publicEntityGalgamesInput struct {
	ID        int    `path:"id" doc:"Official / Tag ID"`
	Page      int    `query:"page" doc:"Page number (default 1)"`
	Limit     int    `query:"limit" doc:"Items per page 1-50 (default 24)"`
	SortField string `query:"sort_field" enum:"created,resource_update_time,view" doc:"Sort field"`
	SortOrder string `query:"sort_order" enum:"asc,desc" doc:"Sort direction"`
}
type publicOfficialGalgamesOutput struct {
	Body publicEnvelope[dto.OfficialGalgamesResponse]
}
type publicTagGalgamesOutput struct {
	Body publicEnvelope[dto.TagGalgamesResponse]
}

// publicEnvelope mirrors the house response envelope {code, message, data}.
type publicEnvelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

// SetupGalgamePublicSpec registers the /v1/galgame public projection operations
// to derive the frozen public OpenAPI. Handlers are stubs (Fiber serves the live
// paths); this only shapes the spec.
func SetupGalgamePublicSpec(app *fiber.App) huma.API {
	cfg := huma.DefaultConfig("NextMoe Open API — Galgame", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	api := humafiber.New(app, cfg)

	tags := []string{"galgame-public"}
	huma.Register(api, huma.Operation{
		OperationID: "listGalgamesPublic", Method: http.MethodGet, Path: "/v1/galgame",
		Summary: "Cursor-paginated galgame list (thin aggregate items); sort=id|release_date, keyset cursor, no offset", Tags: tags,
	}, func(context.Context, *publicListInput) (*publicListOutput, error) { return &publicListOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgamePublic", Method: http.MethodGet, Path: "/v1/galgame/{id}",
		Summary: "Full aggregate record for one galgame (multi-source merge + attribution); include=intro,scores,covers,taxonomy gates the heavy blocks; weak ETag / 304", Tags: tags,
	}, func(context.Context, *publicDetailInput) (*publicDetailOutput, error) {
		return &publicDetailOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "batchGalgamesPublic", Method: http.MethodGet, Path: "/v1/galgame/batch",
		Summary: "Batch aggregate items by id (view=brief default = thin items; view=detail = full records, no include)", Tags: tags,
	}, func(context.Context, *publicBatchInput) (*publicBatchOutput, error) { return &publicBatchOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "searchGalgamesPublic", Method: http.MethodGet, Path: "/v1/galgame/search",
		Summary: "Meilisearch relevance over published + sfw galgames, projected to thin aggregate items (page/limit)", Tags: tags,
	}, func(context.Context, *publicSearchInput) (*publicSearchOutput, error) {
		return &publicSearchOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameChangesPublic", Method: http.MethodGet, Path: "/v1/galgame/changes",
		Summary: "Incremental-sync keyset stream of {id, updated} ascending by (updated, id); take the ids and hydrate via batch", Tags: tags,
	}, func(context.Context, *publicChangesInput) (*publicChangesOutput, error) {
		return &publicChangesOutput{}, nil
	})

	// Whitelisted passthroughs (existing published DTOs).
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameCalendarPublic", Method: http.MethodGet, Path: "/v1/galgame/calendar",
		Summary: "Release calendar for one ISO month (sfw)", Tags: tags,
	}, func(context.Context, *publicCalendarInput) (*publicCalendarOutput, error) {
		return &publicCalendarOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameCalendarPendingPublic", Method: http.MethodGet, Path: "/v1/galgame/calendar/pending",
		Summary: "Release calendar 'month TBD' bucket for a year (sfw)", Tags: tags,
	}, func(context.Context, *publicCalendarPendingInput) (*publicCalendarPendingOutput, error) {
		return &publicCalendarPendingOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameCalendarTBAPublic", Method: http.MethodGet, Path: "/v1/galgame/calendar/tba",
		Summary: "Release calendar 'date TBA' bucket (sfw)", Tags: tags,
	}, func(context.Context, *publicCalendarTBAInput) (*publicCalendarTBAOutput, error) {
		return &publicCalendarTBAOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listOfficialGalgamesPublic", Method: http.MethodGet, Path: "/v1/galgame/officials/{id}/galgames",
		Summary: "A maker's self-description + a page of its galgames (sfw)", Tags: tags,
	}, func(context.Context, *publicEntityGalgamesInput) (*publicOfficialGalgamesOutput, error) {
		return &publicOfficialGalgamesOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listTagGalgamesPublic", Method: http.MethodGet, Path: "/v1/galgame/tags/{id}/galgames",
		Summary: "A tag's self-description + a page of the galgames carrying it (sfw)", Tags: tags,
	}, func(context.Context, *publicEntityGalgamesInput) (*publicTagGalgamesOutput, error) {
		return &publicTagGalgamesOutput{}, nil
	})
	return api
}
