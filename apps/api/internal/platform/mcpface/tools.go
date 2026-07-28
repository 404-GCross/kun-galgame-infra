package mcpface

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultSearchLimit is the conservative page size the search tools apply when
// the caller omits one, keeping tool payloads small for the model (design §4).
const defaultSearchLimit = 10

// readOnly is the shared annotation set for the M1 tools: every tool is a
// read-only, idempotent GET against an open-world external registry.
var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

// registerTools installs the eight tools on the server (M1 seven + catalog_name_get). Names are unversioned
// (the /v1 contract is versioned upstream); descriptions are English and written
// for the calling LLM, with the lookup-vs-search division spelled out.
func registerTools(s *mcp.Server, up *Upstream) {
	t := &tools{up: up}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "galgame_search",
		Title:       "Search galgames",
		Description: descGalgameSearch,
		Annotations: readOnly,
	}, t.galgameSearch)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "galgame_get",
		Title:       "Get a galgame by id",
		Description: descGalgameGet,
		Annotations: readOnly,
	}, t.galgameGet)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "catalog_search",
		Title:       "Search catalog entities",
		Description: descCatalogSearch,
		Annotations: readOnly,
	}, t.catalogSearch)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "catalog_work_get",
		Title:       "Get a catalog work by id",
		Description: descCatalogWorkGet,
		Annotations: readOnly,
	}, t.catalogWorkGet)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "catalog_lookup_external",
		Title:       "Look up a work by external id",
		Description: descCatalogLookup,
		Annotations: readOnly,
	}, t.catalogLookupExternal)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "catalog_name_get",
		Title:       "Get a catalog credited name by id",
		Description: descCatalogNameGet,
		Annotations: readOnly,
	}, t.catalogNameGet)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "catalog_label_get",
		Title:       "Get a catalog label by id",
		Description: descCatalogLabelGet,
		Annotations: readOnly,
	}, t.catalogLabelGet)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "catalog_character_get",
		Title:       "Get a catalog character by id",
		Description: descCatalogCharacterGet,
		Annotations: readOnly,
	}, t.catalogCharacterGet)
}

// ─────────────────────────── galgame face ───────────────────────────

const descGalgameSearch = "Full-text search the NextMoe galgame aggregate (titles, aliases) and return " +
	"relevance-ranked brief records. Use this for NATURAL-LANGUAGE queries ('find visual novels by a maker', " +
	"a fuzzy title). If you already have an external id like a VNDB id, use catalog_lookup_external instead. " +
	"To fetch one game's full record, take an id from a hit and call galgame_get."

type galgameSearchInput struct {
	Q                string `json:"q,omitempty" jsonschema:"Full-text query over localized titles and aliases (Japanese / Simplified & Traditional Chinese / English). Omit to browse by filters and sort only."`
	Sort             string `json:"sort,omitempty" jsonschema:"Result ordering, e.g. relevance (default), released_desc, released_asc, updated_desc."`
	Page             int    `json:"page,omitempty" jsonschema:"1-based page number for relevance paging (default 1)."`
	Limit            int    `json:"limit,omitempty" jsonschema:"Results per page (default 10). Upstream caps apply."`
	TagIDs           string `json:"tag_ids,omitempty" jsonschema:"Comma-separated tag ids to filter by (AND)."`
	OfficialIDs      string `json:"official_ids,omitempty" jsonschema:"Comma-separated official (brand / maker) ids to filter by."`
	EngineIDs        string `json:"engine_ids,omitempty" jsonschema:"Comma-separated engine ids to filter by."`
	SeriesID         int    `json:"series_id,omitempty" jsonschema:"Restrict results to a single series id."`
	OriginalLanguage string `json:"original_language,omitempty" jsonschema:"Comma-separated original-language codes, e.g. ja,zh,en."`
	ReleasedFrom     string `json:"released_from,omitempty" jsonschema:"Lower bound on release date (YYYY-MM-DD or YYYY)."`
	ReleasedTo       string `json:"released_to,omitempty" jsonschema:"Upper bound on release date (YYYY-MM-DD or YYYY)."`
	Include          string `json:"include,omitempty" jsonschema:"Comma-separated blocks to expand on each item: officials,scores,meta,intro (default none; unknown names ignored)."`
	Fields           string `json:"fields,omitempty" jsonschema:"Comma-separated top-level response keys to return (sparse fieldset); id is always included, unknown names ignored."`
	ContentLimit     string `json:"content_limit,omitempty" jsonschema:"Content filter: sfw (default) / nsfw / all. nsfw and all require a key with the galgame:nsfw scope; otherwise silently coerced to sfw."`
	AgeLimit         string `json:"age_limit,omitempty" jsonschema:"Filter by age rating: all or r18."`
	ReleasedMonths   string `json:"released_months,omitempty" jsonschema:"Comma-separated months 1-12 (seasonal filter)."`
	Facets           bool   `json:"facets,omitempty" jsonschema:"When true, add the facet distribution (tags / officials / engines / languages) to the response."`
}

func (t *tools) galgameSearch(ctx context.Context, req *mcp.CallToolRequest, in galgameSearchInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "q", in.Q)
	setStr(q, "sort", in.Sort)
	setInt(q, "page", in.Page)
	limit := in.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	setInt(q, "limit", limit)
	setStr(q, "tag_ids", in.TagIDs)
	setStr(q, "official_ids", in.OfficialIDs)
	setStr(q, "engine_ids", in.EngineIDs)
	setInt(q, "series_id", in.SeriesID)
	setStr(q, "original_language", in.OriginalLanguage)
	setStr(q, "released_from", in.ReleasedFrom)
	setStr(q, "released_to", in.ReleasedTo)
	setStr(q, "include", in.Include)
	setStr(q, "fields", in.Fields)
	setStr(q, "content_limit", in.ContentLimit)
	setStr(q, "age_limit", in.AgeLimit)
	setStr(q, "released_months", in.ReleasedMonths)
	setBool(q, "facets", in.Facets)
	return t.run(ctx, req, "galgame_search", "/v1/galgame/search", q)
}

const descGalgameGet = "Fetch one galgame's full aggregate record by its numeric id: merged names, intro, covers, " +
	"screenshots, tags, brands, engines, release dates and cross-source scores. The record carries a " +
	"catalog_work_id linking it into the cross-media identity registry (use catalog_work_get for that face)."

type galgameGetInput struct {
	ID           int    `json:"id" jsonschema:"The galgame id (required). Find one with galgame_search or catalog_lookup_external."`
	Include      string `json:"include,omitempty" jsonschema:"Comma-separated heavy blocks to embed: links,screenshots,series,meta,taxonomy,tag_refs,official_refs,engine_refs,intro,scores,covers."`
	Fields       string `json:"fields,omitempty" jsonschema:"Comma-separated top-level response keys to return (sparse fieldset); id is always included, unknown names ignored."`
	ContentLimit string `json:"content_limit,omitempty" jsonschema:"Content filter: sfw (default) / nsfw / all. nsfw and all require a key with the galgame:nsfw scope; otherwise silently coerced to sfw. A detail 404s only when the resolved filter cannot cover the row's rating."`
}

func (t *tools) galgameGet(ctx context.Context, req *mcp.CallToolRequest, in galgameGetInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "include", in.Include)
	setStr(q, "fields", in.Fields)
	setStr(q, "content_limit", in.ContentLimit)
	return t.run(ctx, req, "galgame_get", pathID("/v1/galgame", in.ID), q)
}

// ─────────────────────────── catalog face ───────────────────────────

const descCatalogSearch = "Search the cross-media identity registry for entities by name. Choose the index with `type`: " +
	"names (creator credit-names / persons), characters, labels (brands / doujin circles), or works " +
	"(work titles across media; r18 hits excluded unless nsfw=true). Use this for " +
	"NATURAL-LANGUAGE lookup; when you already hold an external id use catalog_lookup_external, and to fetch a " +
	"work's full registry row use catalog_work_get."

type catalogSearchInput struct {
	Type   string `json:"type" jsonschema:"Which entity index to search (required): one of names, characters, labels, or works."`
	Q      string `json:"q,omitempty" jsonschema:"Relevance query over the entity's names."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max hits (default 20, hard cap 20)."`
	Locale string `json:"locale,omitempty" jsonschema:"UI locale pinning the query language: zh, ja, or en."`
	Nsfw   bool   `json:"nsfw,omitempty" jsonschema:"works only: true = include r18 hits (default false = excluded server-side)."`
}

func (t *tools) catalogSearch(ctx context.Context, req *mcp.CallToolRequest, in catalogSearchInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "type", in.Type)
	setStr(q, "q", in.Q)
	setInt(q, "limit", in.Limit)
	setStr(q, "locale", in.Locale)
	setBool(q, "nsfw", in.Nsfw)
	return t.run(ctx, req, "catalog_search", "/v1/catalog/search", q)
}

const descCatalogWorkGet = "Fetch one catalog work's registry row by its numeric catalog work id: the canonical cross-media " +
	"identity plus external-source anchors. Pass include=credits and/or include=relations to embed the credited " +
	"staff/cast and the linked works in the same response."

type catalogWorkGetInput struct {
	ID      int    `json:"id" jsonschema:"The catalog work id (required)."`
	Include string `json:"include,omitempty" jsonschema:"Comma-separated extra blocks: credits and/or relations. Omit for the bare registry row."`
	Nsfw    bool   `json:"nsfw,omitempty" jsonschema:"true = serve r18 works and r18 relation ends (caller-controlled; default false = hidden)."`
}

func (t *tools) catalogWorkGet(ctx context.Context, req *mcp.CallToolRequest, in catalogWorkGetInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "include", in.Include)
	setBool(q, "nsfw", in.Nsfw)
	return t.run(ctx, req, "catalog_work_get", pathID("/v1/catalog/works", in.ID), q)
}

const descCatalogLookup = "Reverse-look up a catalog work by an EXTERNAL id — the go-to tool when you already have an id " +
	"from another database. Give the source and its id (e.g. source=vndb, external_id=v19658); returns the matched " +
	"work plus claim pointers. Prefer this over any search when an external id is available."

type catalogLookupInput struct {
	Source     string `json:"source" jsonschema:"External source key (required): vndb, bangumi, dlsite, or erogamescape."`
	ExternalID string `json:"external_id" jsonschema:"The id within that source (required), e.g. v19658 for VNDB."`
	Nsfw       bool   `json:"nsfw,omitempty" jsonschema:"true = resolve r18 works too (default false = 404 on an r18 hit)."`
}

func (t *tools) catalogLookupExternal(ctx context.Context, req *mcp.CallToolRequest, in catalogLookupInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "source", in.Source)
	setStr(q, "external_id", in.ExternalID)
	setBool(q, "nsfw", in.Nsfw)
	return t.run(ctx, req, "catalog_lookup_external", "/v1/catalog/lookup", q)
}

const descCatalogNameGet = "Fetch one credited name (creator identity) by its numeric id: localized intros[] (creator " +
	"bio), siblings[] (same-person alternate names) and external refs. Pass include=credits to attach the " +
	"works this name is credited on, with roles."

type catalogNameGetInput struct {
	ID      int    `json:"id" jsonschema:"The name id (required). Find one with catalog_search type=names."`
	Include string `json:"include,omitempty" jsonschema:"Set to credits to attach the works this name is credited on."`
	Nsfw    bool   `json:"nsfw,omitempty" jsonschema:"true = include r18 works among the credits (default false = dropped)."`
	Limit   int    `json:"limit,omitempty" jsonschema:"Max attached credits (default 50, cap 50)."`
	Offset  int    `json:"offset,omitempty" jsonschema:"Offset into the attached credits list."`
}

func (t *tools) catalogNameGet(ctx context.Context, req *mcp.CallToolRequest, in catalogNameGetInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "include", in.Include)
	setBool(q, "nsfw", in.Nsfw)
	setInt(q, "limit", in.Limit)
	setInt(q, "offset", in.Offset)
	return t.run(ctx, req, "catalog_name_get", pathID("/v1/catalog/names", in.ID), q)
}

const descCatalogLabelGet = "Fetch one label (brand / doujin circle) by its numeric id: display names, intros[] and links[]. " +
	"Pass include=works to attach the works attributed to the label."

type catalogLabelGetInput struct {
	ID      int    `json:"id" jsonschema:"The label id (required)."`
	Include string `json:"include,omitempty" jsonschema:"Set to works to attach the works attributed to this label."`
	Nsfw    bool   `json:"nsfw,omitempty" jsonschema:"true = include r18 works among the attributions (default false = dropped)."`
	Limit   int    `json:"limit,omitempty" jsonschema:"Max attached works (default 50, cap 50)."`
	Offset  int    `json:"offset,omitempty" jsonschema:"Offset into the attached works list."`
}

func (t *tools) catalogLabelGet(ctx context.Context, req *mcp.CallToolRequest, in catalogLabelGetInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "include", in.Include)
	setInt(q, "limit", in.Limit)
	setBool(q, "nsfw", in.Nsfw)
	setInt(q, "offset", in.Offset)
	return t.run(ctx, req, "catalog_label_get", pathID("/v1/catalog/labels", in.ID), q)
}

const descCatalogCharacterGet = "Fetch one character by its numeric id: localized intros[] (bio), a portrait image URL, " +
	"and external refs. Traits are spoiler-gated: pass spoilers=1|2 to raise the max spoiler level (default " +
	"0 = safe). Pass include=works to attach the works the character appears in (with voice-actor names)."

type catalogCharacterGetInput struct {
	ID       int    `json:"id" jsonschema:"The character id (required)."`
	Include  string `json:"include,omitempty" jsonschema:"Set to works to attach the works the character appears in."`
	Nsfw     bool   `json:"nsfw,omitempty" jsonschema:"true = include r18 works and sexual-family traits (default false = both dropped)."`
	Spoilers int    `json:"spoilers,omitempty" jsonschema:"Max trait spoiler level 0-2 (default 0 = safe)."`
	Limit    int    `json:"limit,omitempty" jsonschema:"Max attached works (default 50, cap 50)."`
	Offset   int    `json:"offset,omitempty" jsonschema:"Offset into the attached works list."`
}

func (t *tools) catalogCharacterGet(ctx context.Context, req *mcp.CallToolRequest, in catalogCharacterGetInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "include", in.Include)
	setInt(q, "limit", in.Limit)
	setBool(q, "nsfw", in.Nsfw)
	setInt(q, "spoilers", in.Spoilers)
	setInt(q, "offset", in.Offset)
	return t.run(ctx, req, "catalog_character_get", pathID("/v1/catalog/characters", in.ID), q)
}
