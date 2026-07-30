package mcpface

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// readOnly is the shared annotation set for the M1 tools: every tool is a
// read-only, idempotent GET against an open-world external registry.
var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

// registerTools installs the nine tools on the server (M1 five surviving +
// catalog_name_get + the canonical-W1 trio: works-list / changes / tag). Names are unversioned
// (the /v1 contract is versioned upstream); descriptions are English and written
// for the calling LLM, with the lookup-vs-search division spelled out.
//
// The two galgame_* tools retired at wave 146 (2026-07-30) together with the
// /v1/galgame face they proxied: that face now answers 410 Gone, so keeping the
// tools registered would only hand the calling model a guaranteed error. Their
// successors are catalog_search (type=works) and catalog_work_get on the
// canonical /v1/catalog face.
func registerTools(s *mcp.Server, up *Upstream) {
	t := &tools{up: up}

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

	mcp.AddTool(s, &mcp.Tool{
		Name:        "catalog_works_list",
		Title:       "Browse / filter catalog works",
		Description: descCatalogWorksList,
		Annotations: readOnly,
	}, t.catalogWorksList)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "catalog_changes",
		Title:       "Poll the catalog change feed",
		Description: descCatalogChanges,
		Annotations: readOnly,
	}, t.catalogChanges)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "catalog_tag_get",
		Title:       "Get a canonical tag by id",
		Description: descCatalogTagGet,
		Annotations: readOnly,
	}, t.catalogTagGet)
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

const descCatalogWorksList = "Browse / filter the catalog works registry — the bulk lane. Filter by content_rating / " +
	"claimed / label_id / tag_id / series_id / platform / release window; ids=comma-list (max 100) batch-hydrates " +
	"known ids in one call. sort=id (stable browse, default) or updated (newest-updated first). Keyset-paginated: " +
	"pass the returned next_cursor to continue. For NATURAL-LANGUAGE title search use catalog_search type=works."

type catalogWorksListInput struct {
	ContentRating  string `json:"content_rating,omitempty" jsonschema:"Filter by rating: all_ages, sensitive, or r18 (r18 additionally requires nsfw=true)."`
	Claimed        string `json:"claimed,omitempty" jsonschema:"true = claimed works only; false = bodyless only; omit = both."`
	LabelID        int    `json:"label_id,omitempty" jsonschema:"Only works attributed to this label id."`
	TagID          int    `json:"tag_id,omitempty" jsonschema:"Only works carrying a source tag mapped to this canonical tag id."`
	SeriesID       int    `json:"series_id,omitempty" jsonschema:"Only member works of this series id."`
	Platform       string `json:"platform,omitempty" jsonschema:"vndb platform code, e.g. win, and, ios."`
	ReleasedAfter  string `json:"released_after,omitempty" jsonschema:"YYYY-MM-DD inclusive lower bound on the earliest release date per work."`
	ReleasedBefore string `json:"released_before,omitempty" jsonschema:"YYYY-MM-DD inclusive upper bound."`
	IDs            string `json:"ids,omitempty" jsonschema:"Comma-separated work ids (max 100) — batch-hydrate known ids in one call."`
	Sort           string `json:"sort,omitempty" jsonschema:"id = ascending browse order (default); updated = newest-updated first."`
	Cursor         string `json:"cursor,omitempty" jsonschema:"Opaque keyset cursor from a prior next_cursor; omit for the first page."`
	Limit          int    `json:"limit,omitempty" jsonschema:"Items per page 1-100 (default 20)."`
	Nsfw           bool   `json:"nsfw,omitempty" jsonschema:"true = include r18 works (default false = dropped server-side)."`
}

func (t *tools) catalogWorksList(ctx context.Context, req *mcp.CallToolRequest, in catalogWorksListInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "content_rating", in.ContentRating)
	setStr(q, "claimed", in.Claimed)
	setInt(q, "label_id", in.LabelID)
	setInt(q, "tag_id", in.TagID)
	setInt(q, "series_id", in.SeriesID)
	setStr(q, "platform", in.Platform)
	setStr(q, "released_after", in.ReleasedAfter)
	setStr(q, "released_before", in.ReleasedBefore)
	setStr(q, "ids", in.IDs)
	setStr(q, "sort", in.Sort)
	setStr(q, "cursor", in.Cursor)
	setInt(q, "limit", in.Limit)
	setBool(q, "nsfw", in.Nsfw)
	return t.run(ctx, req, "catalog_works_list", "/v1/catalog/works", q)
}

const descCatalogChanges = "Poll the catalog change feed for INCREMENTAL SYNC: keyset-paginated entries for works " +
	"whose records changed, oldest first. Store the returned next_cursor and pass it on the next poll to receive " +
	"only what changed since. entity_type currently supports work (default)."

type catalogChangesInput struct {
	EntityType string `json:"entity_type,omitempty" jsonschema:"Feed scope: work (default)."`
	Cursor     string `json:"cursor,omitempty" jsonschema:"Opaque keyset cursor; omit to start from the beginning."`
	Limit      int    `json:"limit,omitempty" jsonschema:"Items per page 1-500 (default 100)."`
}

func (t *tools) catalogChanges(ctx context.Context, req *mcp.CallToolRequest, in catalogChangesInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "entity_type", in.EntityType)
	setStr(q, "cursor", in.Cursor)
	setInt(q, "limit", in.Limit)
	return t.run(ctx, req, "catalog_changes", "/v1/catalog/changes", q)
}

const descCatalogTagGet = "Fetch one canonical tag (the cross-source tag vocabulary) by its numeric id. Pass " +
	"include=works to attach the works carrying any source tag mapped to it (limit/offset paginated)."

type catalogTagGetInput struct {
	ID      int    `json:"id" jsonschema:"The canonical tag id (required)."`
	Include string `json:"include,omitempty" jsonschema:"Set to works to attach works carrying any mapped source tag."`
	Nsfw    bool   `json:"nsfw,omitempty" jsonschema:"true = include r18 works among the attachments (default false = dropped)."`
	Limit   int    `json:"limit,omitempty" jsonschema:"Works per page 1-50 (default 50)."`
	Offset  int    `json:"offset,omitempty" jsonschema:"Rows to skip."`
}

func (t *tools) catalogTagGet(ctx context.Context, req *mcp.CallToolRequest, in catalogTagGetInput) (*mcp.CallToolResult, any, error) {
	q := newQuery()
	setStr(q, "include", in.Include)
	setBool(q, "nsfw", in.Nsfw)
	setInt(q, "limit", in.Limit)
	setInt(q, "offset", in.Offset)
	return t.run(ctx, req, "catalog_tag_get", pathID("/v1/catalog/tags", in.ID), q)
}
