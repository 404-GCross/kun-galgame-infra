// NextMoe open-API galgame taxonomy public projection (W1b) — SPEC ONLY.
//
// Companion to public_huma.go: these register the 14 curated taxonomy operations
// (tags 5 / officials 4 / engines 3 / series 2) on the same public Huma API so
// cmd/gen-openapi -galgame-public derives them into the frozen v1 spec from the
// dto.Public* types the Fiber handlers in public_taxonomy_handler.go return.
// Never mounted on the live service. registerGalgameTaxonomyPublicOps is called
// from SetupGalgamePublicSpec.
package handler

import (
	"context"
	"net/http"

	"api/internal/platform/galgame/dto"

	"github.com/danielgtaylor/huma/v2"
)

// ─────────────── input types (query/path params for the public spec) ───────────────

type publicTaxListInput struct {
	Page  int `query:"page" doc:"Page number (default 1)"`
	Limit int `query:"limit" doc:"Items per page 1-100 (default 50)"`
}

type publicTagListInput struct {
	Page         int    `query:"page" doc:"Page number (default 1)"`
	Limit        int    `query:"limit" doc:"Items per page 1-100 (default 50)"`
	ContentLimit string `query:"content_limit" doc:"Content filter: sfw (default) | nsfw | all. nsfw/all require a key with the galgame:nsfw scope (otherwise silently coerced to sfw). sfw hides the sexual tag category."`
}

type publicSeriesListInput struct {
	Page         int    `query:"page" doc:"Page number (default 1)"`
	Limit        int    `query:"limit" doc:"Items per page 1-50 (default 24)"`
	ContentLimit string `query:"content_limit" doc:"Content filter: sfw (default) | nsfw | all. Gates each series' member preview + galgame_count. nsfw/all require the galgame:nsfw scope; otherwise silently coerced to sfw."`
}

type publicTagMultiInput struct {
	IDs          string `query:"ids" doc:"Comma-separated tag ids; returns the published galgames carrying ALL of them (AND intersection)"`
	Page         int    `query:"page" doc:"Page number (default 1)"`
	Limit        int    `query:"limit" doc:"Items per page 1-50 (default 24)"`
	ContentLimit string `query:"content_limit" doc:"Content filter: sfw (default) | nsfw | all. nsfw/all require the galgame:nsfw scope; otherwise silently coerced to sfw."`
}

type publicTagSearchInput struct {
	Q        string `query:"q" doc:"Free-text query across tag names + aliases"`
	Category string `query:"category" doc:"Filter by tag category: content | sexual | technical"`
	Limit    int    `query:"limit" doc:"Max hits 1-100 (default 50)"`
}

type publicOfficialSearchInput struct {
	Q        string `query:"q" doc:"Free-text query across maker names + aliases"`
	Category string `query:"category" doc:"Filter by maker category: company | individual | amateur"`
	Lang     string `query:"lang" doc:"Filter by maker primary language (BCP-47 tag)"`
	Limit    int    `query:"limit" doc:"Max hits 1-100 (default 50)"`
}

type publicTaxEntityInput struct {
	ID int `path:"id" doc:"Entity id"`
}

type publicSeriesDetailInput struct {
	ID           int    `path:"id" doc:"Series id"`
	ContentLimit string `query:"content_limit" doc:"Content filter: sfw (default) | nsfw | all. Gates the embedded member set + galgame_count. nsfw/all require the galgame:nsfw scope; otherwise silently coerced to sfw."`
}

// ─────────────── output types (curated public DTOs) ───────────────

type publicTagListOutput struct {
	Body publicEnvelope[dto.PublicTagListData]
}
type publicTagSearchOutput struct {
	Body publicEnvelope[dto.PublicTagSearchData]
}
type publicTagMultiOutput struct {
	Body publicEnvelope[dto.PublicItemListData]
}
type publicTagOutput struct {
	Body publicEnvelope[dto.PublicTagEntity]
}
type publicOfficialListOutput struct {
	Body publicEnvelope[dto.PublicOfficialListData]
}
type publicOfficialSearchOutput struct {
	Body publicEnvelope[dto.PublicOfficialSearchData]
}
type publicOfficialOutput struct {
	Body publicEnvelope[dto.PublicOfficialEntity]
}
type publicEngineListOutput struct {
	Body publicEnvelope[dto.PublicEngineListData]
}
type publicEngineOutput struct {
	Body publicEnvelope[dto.PublicEngineEntity]
}
type publicSeriesListOutput struct {
	Body publicEnvelope[dto.PublicSeriesListData]
}
type publicSeriesOutput struct {
	Body publicEnvelope[dto.PublicSeriesEntity]
}
type publicIDsOutput struct {
	Body publicEnvelope[dto.PublicIDsData]
}

// registerGalgameTaxonomyPublicOps registers the 14 curated taxonomy operations
// on the public Huma API (SPEC ONLY). Handlers are stubs — Fiber serves the live
// paths (public_taxonomy_handler.go); this only shapes the frozen spec.
func registerGalgameTaxonomyPublicOps(api huma.API, tags []string) {
	// ── Tags (5) ──
	huma.Register(api, huma.Operation{
		OperationID: "listTagsPublic", Method: http.MethodGet, Path: "/v1/galgame/tags",
		Summary: "Curated tag list (page/limit); sfw hides the sexual category", Tags: tags,
	}, func(context.Context, *publicTagListInput) (*publicTagListOutput, error) {
		return &publicTagListOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "searchTagsPublic", Method: http.MethodGet, Path: "/v1/galgame/tags/search",
		Summary: "Meilisearch relevance over tags (id, name, aliases, category, galgame_count)", Tags: tags,
	}, func(context.Context, *publicTagSearchInput) (*publicTagSearchOutput, error) {
		return &publicTagSearchOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "multiTagsPublic", Method: http.MethodGet, Path: "/v1/galgame/tags/multi",
		Summary: "Published galgames carrying ALL given tag ids (AND intersection), as thin items", Tags: tags,
	}, func(context.Context, *publicTagMultiInput) (*publicTagMultiOutput, error) {
		return &publicTagMultiOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getTagPublic", Method: http.MethodGet, Path: "/v1/galgame/tags/{id}",
		Summary: "One tag's curated record (description, galgame_count, aliases, created, updated)", Tags: tags,
	}, func(context.Context, *publicTaxEntityInput) (*publicTagOutput, error) { return &publicTagOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "listTagGalgameIDsPublic", Method: http.MethodGet, Path: "/v1/galgame/tags/{id}/galgame-ids",
		Summary: "Ids of every published galgame carrying this tag ({ids:[]int})", Tags: tags,
	}, func(context.Context, *publicTaxEntityInput) (*publicIDsOutput, error) { return &publicIDsOutput{}, nil })

	// ── Officials (4) ──
	huma.Register(api, huma.Operation{
		OperationID: "listOfficialsPublic", Method: http.MethodGet, Path: "/v1/galgame/officials",
		Summary: "Curated maker list (page/limit)", Tags: tags,
	}, func(context.Context, *publicTaxListInput) (*publicOfficialListOutput, error) {
		return &publicOfficialListOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "searchOfficialsPublic", Method: http.MethodGet, Path: "/v1/galgame/officials/search",
		Summary: "Meilisearch relevance over makers (id, name, aliases, category, galgame_count, original, lang)", Tags: tags,
	}, func(context.Context, *publicOfficialSearchInput) (*publicOfficialSearchOutput, error) {
		return &publicOfficialSearchOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getOfficialPublic", Method: http.MethodGet, Path: "/v1/galgame/officials/{id}",
		Summary: "One maker's curated record (original, link, lang, description, galgame_count, aliases)", Tags: tags,
	}, func(context.Context, *publicTaxEntityInput) (*publicOfficialOutput, error) {
		return &publicOfficialOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listOfficialGalgameIDsPublic", Method: http.MethodGet, Path: "/v1/galgame/officials/{id}/galgame-ids",
		Summary: "Ids of every published galgame under this maker ({ids:[]int})", Tags: tags,
	}, func(context.Context, *publicTaxEntityInput) (*publicIDsOutput, error) { return &publicIDsOutput{}, nil })

	// ── Engines (3) ──
	huma.Register(api, huma.Operation{
		OperationID: "listEnginesPublic", Method: http.MethodGet, Path: "/v1/galgame/engines",
		Summary: "Curated engine list (page/limit)", Tags: tags,
	}, func(context.Context, *publicTaxListInput) (*publicEngineListOutput, error) {
		return &publicEngineListOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getEnginePublic", Method: http.MethodGet, Path: "/v1/galgame/engines/{id}",
		Summary: "One engine's curated record (description, alias, galgame_count)", Tags: tags,
	}, func(context.Context, *publicTaxEntityInput) (*publicEngineOutput, error) {
		return &publicEngineOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listEngineGalgameIDsPublic", Method: http.MethodGet, Path: "/v1/galgame/engines/{id}/galgame-ids",
		Summary: "Ids of every published galgame on this engine ({ids:[]int})", Tags: tags,
	}, func(context.Context, *publicTaxEntityInput) (*publicIDsOutput, error) { return &publicIDsOutput{}, nil })

	// ── Series (2) ──
	huma.Register(api, huma.Operation{
		OperationID: "listSeriesPublic", Method: http.MethodGet, Path: "/v1/galgame/series",
		Summary: "Curated series list (page/limit) with content_limit-gated member previews (thin items)", Tags: tags,
	}, func(context.Context, *publicSeriesListInput) (*publicSeriesListOutput, error) {
		return &publicSeriesListOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getSeriesPublic", Method: http.MethodGet, Path: "/v1/galgame/series/{id}",
		Summary: "One series' curated record (description, galgame_count) + its member set as thin items", Tags: tags,
	}, func(context.Context, *publicSeriesDetailInput) (*publicSeriesOutput, error) {
		return &publicSeriesOutput{}, nil
	})
}
