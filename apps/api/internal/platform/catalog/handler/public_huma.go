// NextMoe open-API catalog public projection (/v1/catalog/*) — SPEC ONLY.
//
// Like s2s.go / admin.go, these operations are SERVED by the Fiber handlers in
// public_handler.go; this file registers them on a Huma API purely so
// cmd/gen-openapi -catalog-public can derive an INDEPENDENT public spec from the
// same dto.Public* types the handlers return — the frozen v1 contract, decoupled
// from the internal S2S spec. Never mounted on the live service.
//
// The runtime-reachable set MUST equal this spec exactly (02 reviewer ruling:
// nothing reachable on the frozen /v1 face may be absent from the published
// spec). Every route in setupPublicCatalog has an operation here and vice versa.
package handler

import (
	"context"
	"net/http"

	"api/internal/platform/catalog/dto"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// ─────────────── input types (query/path/body params for the public spec) ───────────────

type publicWorkInput struct {
	ID       int64  `path:"id" doc:"Catalog work id"`
	Include  string `query:"include" doc:"Comma-separated heavy blocks: relations,credits (default: none)"`
	NSFW     bool   `query:"nsfw" doc:"true/1 = serve r18 works and r18 relation ends (caller-controlled; default false = hidden)"`
	Spoilers int16  `query:"spoilers" doc:"Max tag spoiler level 0-2 (default 0 = safe): tags[] carries per-edge spoiler + per-tag sexual flags, and rows above this ceiling are omitted entirely. The axis is populated for the VNDB-derived vocabulary only — Bangumi/DLsite folksonomy publishes no spoiler or category concept, so those rows read 0/false"`
}
type publicWorkOutput struct {
	Body Envelope[dto.PublicCatalogWork]
}

type publicLookupInput struct {
	Source     string `query:"source" doc:"Upstream source key: vndb | bangumi | dlsite | erogamescape (the internal registry spelling erogamespace is accepted too)"`
	ExternalID string `query:"external_id" doc:"The id within that source. type=work accepts vndb v19658 or 19658 (and dlsite RJ/VJ numbers); the non-work types match VERBATIM as the registry stores them — vndb character c1234, label p129, staff a bare number"`
	Type       string `query:"type" enum:"work,name,character,label" default:"work" doc:"Which entity family the external id is resolved against (default work); an unknown token is a 400, whereas an unknown SOURCE is a miss"`
	NSFW       bool   `query:"nsfw" doc:"true/1 = resolve r18 works too (default false = 404 on an r18 hit); on type=character it also keeps sexual traits"`
}
type publicLookupOutput struct {
	Body Envelope[dto.PublicLookupData]
}

type publicLookupBatchInput struct {
	Body dto.PublicLookupBatchRequest
}
type publicLookupBatchOutput struct {
	Body Envelope[dto.PublicLookupBatchData]
}

type publicResolveInput struct {
	Body dto.PublicResolveRequest
}
type publicResolveOutput struct {
	Body Envelope[dto.PublicResolveData]
}

type publicRedirectsInput struct {
	EntityType string `query:"entity_type" doc:"Filter to one entity type: person|name|org|label|character|work|release"`
	Cursor     string `query:"cursor" doc:"Opaque keyset cursor from a prior response's next_cursor; omit for the first page"`
	Limit      int    `query:"limit" doc:"Items per page 1-500 (default 100)"`
}
type publicRedirectsOutput struct {
	Body Envelope[dto.PublicRedirectsData]
}

type publicPersonInput struct {
	ID      int64  `path:"id" doc:"Credit-name id (the addressable credited identity)"`
	Include string `query:"include" doc:"credits = attach the works this name is credited on"`
	NSFW    bool   `query:"nsfw" doc:"true/1 = include r18 works among the credits (default false = dropped)"`
	Limit   int    `query:"limit" doc:"Credits per page 1-50 (default 50); above 50 is clamped to 50, a non-positive or non-numeric value is a 400"`
	Offset  int    `query:"offset" doc:"Rows to skip"`
}
type publicPersonOutput struct {
	Body Envelope[dto.PublicName]
}

type publicCharacterInput struct {
	ID       int64  `path:"id" doc:"Catalog character id"`
	Include  string `query:"include" doc:"works = attach the works this character appears in"`
	NSFW     bool   `query:"nsfw" doc:"true/1 = include r18 works and sexual-family traits (default false = both dropped)"`
	Spoilers int16  `query:"spoilers" doc:"max trait spoiler level 0-2 (default 0 = safe)"`
	Limit    int    `query:"limit" doc:"Works per page 1-50 (default 50); above 50 is clamped to 50, a non-positive or non-numeric value is a 400"`
	Offset   int    `query:"offset" doc:"Rows to skip"`
}
type publicCharacterOutput struct {
	Body Envelope[dto.PublicCharacter]
}

type publicLabelInput struct {
	ID      int64  `path:"id" doc:"Catalog label id"`
	Include string `query:"include" doc:"works = attach the works attributed to this label"`
	NSFW    bool   `query:"nsfw" doc:"true/1 = include r18 works among the attributions (default false = dropped)"`
	Limit   int    `query:"limit" doc:"Works per page 1-50 (default 50); above 50 is clamped to 50, a non-positive or non-numeric value is a 400"`
	Offset  int    `query:"offset" doc:"Rows to skip"`
}
type publicLabelOutput struct {
	Body Envelope[dto.PublicLabel]
}

type publicEntitySearchInput struct {
	Type   string `query:"type" enum:"names,characters,labels,works,tags" doc:"Which index to search; works (wave 105) searches every LIVE galgame registry work by any title, tags (A2-1d) the canonical cross-source tag vocabulary"`
	Q      string `query:"q" doc:"Search text; empty returns the most-credited entities"`
	Locale string `query:"locale" enum:"zh,ja,en" doc:"UI locale; the server pins the query language"`
	Limit  int    `query:"limit" doc:"Max hits (capped at 20)"`
	NSFW   bool   `query:"nsfw" doc:"works only: true/1 = include r18 hits (default false = excluded server-side)"`
}
type publicEntitySearchOutput struct {
	Body Envelope[dto.PublicEntitySearchData]
}

// ── A2-1d works product search ───────────────────────────────────────────────
//
// A SECOND search door, disjoint from /v1/catalog/search: that one is the
// entity autocomplete (20 flat hits, no paging), this one is the results page a
// galgame site renders. Every filter below is the works LIST parameter of the
// same name with the same meaning, so a query moves between the browse lane and
// the search lane by changing the path only.

type publicWorksSearchInput struct {
	Q              string `query:"q" doc:"Free text over every indexed title / alias of a work (search hints included — findability only). A query that is EXACTLY a VNDB work id (v19658) short-circuits to that one work via its exact anchor instead of full-text, which would prefix-bleed (v1965 also matches v19650). Empty = a filter-only browse ordered by popularity"`
	ContentRating  string `query:"content_rating" enum:"all_ages,sensitive,r18" doc:"Filter by rating (r18 additionally requires nsfw=1)"`
	Claimed        string `query:"claimed" enum:"true,false" doc:"true = claimed works only; false = bodyless only; absent = both"`
	LabelID        int64  `query:"label_id" doc:"Only works attributed to this label"`
	TagID          string `query:"tag_id" doc:"Only works carrying a source tag mapped to this canonical tag; up to 10 comma-separated ids are ANDed (a work must carry all of them), more than 10 or a non-positive/non-numeric entry is a 400"`
	SeriesID       int64  `query:"series_id" doc:"Only member works of this series"`
	EngineID       int64  `query:"engine_id" doc:"Only works built with this engine"`
	ReleasedAfter  string `query:"released_after" doc:"YYYY-MM-DD, inclusive, over the EARLIEST release date per work — the same anchor the works list filters and the calendar buckets on"`
	ReleasedBefore string `query:"released_before" doc:"YYYY-MM-DD, inclusive"`
	OLang          string `query:"olang" doc:"Original-language gate: comma-separated olang values in the upstream BCP-47 spelling (ja, zh-Hans, en, …) or 'all' to switch it off. Default = the ja + zh* family. olang is an OPEN vocabulary, so an unrecognized value yields an empty result, never a 400"`
	Sort           string `query:"sort" enum:"relevance,released_desc,released_asc,updated,popularity" doc:"relevance (default; an empty q degenerates to popularity), released_desc/asc over the earliest release date (works with no dated release sort last in BOTH directions), updated = newest-updated first, popularity = the cross-source signal log1p(max(bangumi collect shelf, DLsite downloads))"`
	Facets         string `query:"facets" doc:"Comma-separated CLOSED vocabulary: content_rating,olang,claimed,tag_id,label_id,engine_id,series_id,source. An unknown token is a 400. Each distribution is counted over the SAME filtered set as total and is keyed by the values you would pass back to that very filter (content_rating counts use the public strings, not enum ints). At most 100 values per facet"`
	Page           int    `query:"page" doc:"1-based page number (default 1); a non-positive or non-numeric value is a 400. A page past the end is an empty page"`
	Limit          int    `query:"limit" doc:"Items per page 1-100 (default 20); above 100 is clamped to 100, a non-positive or non-numeric value is a 400"`
	NSFW           bool   `query:"nsfw" doc:"true/1 = include r18 works (default false = dropped from items, total AND facets alike)"`
	Include        string `query:"include" doc:"Comma-separated rich-brief blocks: names,intros,labels,ratings,covers,refs — the works-list vocabulary verbatim (unknown tokens ignored)"`
}
type publicWorksSearchOutput struct {
	Body Envelope[dto.PublicWorksSearchData]
}

type publicWorksListInput struct {
	ContentRating  string `query:"content_rating" enum:"all_ages,sensitive,r18" doc:"Filter by rating (r18 additionally requires nsfw=1)"`
	Claimed        string `query:"claimed" enum:"true,false" doc:"true = claimed works only; false = bodyless only; absent = both"`
	LabelID        int64  `query:"label_id" doc:"Only works attributed to this label (the catalog_work_label edge)"`
	TagID          string `query:"tag_id" doc:"Only works carrying a source tag mapped to this canonical tag; up to 10 comma-separated ids are ANDed (a work must carry all of them), more than 10 or a non-positive/non-numeric entry is a 400"`
	SeriesID       int64  `query:"series_id" doc:"Only member works of this series"`
	EngineID       int64  `query:"engine_id" doc:"Only works built with this engine (the catalog_work_engine edge); browse the ids via GET /v1/catalog/engines"`
	Platform       string `query:"platform" doc:"vndb platform code (win/and/ios/...) — release-level and work-level rows unioned"`
	ReleasedAfter  string `query:"released_after" doc:"YYYY-MM-DD, inclusive, over the EARLIEST release date per work"`
	ReleasedBefore string `query:"released_before" doc:"YYYY-MM-DD, inclusive"`
	IDs            string `query:"ids" doc:"Comma-separated work ids (max 100) — the batch-hydrate lane"`
	Sort           string `query:"sort" enum:"id,updated" doc:"id = ascending browse order (default); updated = newest-updated first"`
	Cursor         string `query:"cursor" doc:"Opaque keyset cursor from a prior next_cursor; omit for the first page"`
	Limit          int    `query:"limit" doc:"Items per page 1-100 (default 20); above 100 is clamped to 100, a non-positive or non-numeric value is a 400"`
	NSFW           bool   `query:"nsfw" doc:"true/1 = include r18 works (default false = dropped)"`
	Include        string `query:"include" doc:"Comma-separated rich-brief blocks: names,intros,labels,ratings,covers,refs (default: none — the response is then byte-identical to the base contract). Unknown tokens are ignored. names/intros are keyed by the four product locales ja-jp/zh-cn/zh-tw/en-us; covers carries the portrait + banner slots with width/height/thumbhash; refs carries the work exact identity anchors, detail-face shape"`
}
type publicWorksListOutput struct {
	Body Envelope[dto.PublicWorksListData]
}

type publicChangesInput struct {
	EntityType string `query:"entity_type" enum:"work" doc:"v1 feed scope: work (default)"`
	Cursor     string `query:"cursor" doc:"Opaque keyset cursor; omit to start from the beginning"`
	Limit      int    `query:"limit" doc:"Items per page 1-500 (default 100); above 500 is clamped to 500, a non-positive or non-numeric value is a 400"`
}
type publicChangesOutput struct {
	Body Envelope[dto.PublicChangesData]
}

type publicTagInput struct {
	ID      int64  `path:"id" doc:"Canonical tag id (the cross-source tag vocabulary)"`
	Include string `query:"include" doc:"works = attach the works carrying any mapped source tag"`
	NSFW    bool   `query:"nsfw" doc:"true/1 = include r18 works (default false = dropped)"`
	Limit   int    `query:"limit" doc:"Works per page 1-50 (default 50); above 50 is clamped to 50, a non-positive or non-numeric value is a 400"`
	Offset  int    `query:"offset" doc:"Rows to skip"`
}
type publicTagOutput struct {
	Body Envelope[dto.PublicTagDetail]
}

// ── A2-1b taxonomy browse lanes ──────────────────────────────────────────────
//
// The three lanes share the works-list paging posture verbatim (keyset id ASC,
// limit 1-100 default 20, clamp-high / 400-low) and every item carries an
// NSFW-AWARE work_count — the number of works the SAME caller would page
// through via the matching works?<filter>= call.

type publicLabelsListInput struct {
	Kind   string `query:"kind" enum:"game_brand,bunko,publisher,anime_studio,doujin_circle,group" doc:"Filter by label kind; a token outside this closed set is a 400"`
	Cursor string `query:"cursor" doc:"Opaque keyset cursor from a prior next_cursor; omit for the first page"`
	Limit  int    `query:"limit" doc:"Items per page 1-100 (default 20); above 100 is clamped to 100, a non-positive or non-numeric value is a 400"`
	NSFW   bool   `query:"nsfw" doc:"true/1 = count r18 works in work_count (default false = excluded, matching what an sfw works?label_id= call returns)"`
}
type publicLabelsListOutput struct {
	Body Envelope[dto.PublicLabelsListData]
}

type publicTagsListInput struct {
	Tier   string `query:"tier" enum:"core,longtail,hidden" doc:"Filter by display tier; a token outside this closed set is a 400"`
	Kind   string `query:"kind" enum:"content,meta" doc:"Filter by tag kind; a token outside this closed set is a 400"`
	Cursor string `query:"cursor" doc:"Opaque keyset cursor from a prior next_cursor; omit for the first page"`
	Limit  int    `query:"limit" doc:"Items per page 1-100 (default 20); above 100 is clamped to 100, a non-positive or non-numeric value is a 400"`
	NSFW   bool   `query:"nsfw" doc:"true/1 = count r18 works in work_count (default false = excluded, matching what an sfw works?tag_id= call returns)"`
}
type publicTagsListOutput struct {
	Body Envelope[dto.PublicTagsListData]
}

type publicEnginesListInput struct {
	Cursor string `query:"cursor" doc:"Opaque keyset cursor from a prior next_cursor; omit for the first page"`
	Limit  int    `query:"limit" doc:"Items per page 1-100 (default 20); above 100 is clamped to 100, a non-positive or non-numeric value is a 400"`
	NSFW   bool   `query:"nsfw" doc:"true/1 = count r18 works in work_count (default false = excluded, matching what an sfw works?engine_id= call returns)"`
}
type publicEnginesListOutput struct {
	Body Envelope[dto.PublicEnginesListData]
}

type publicEngineInput struct {
	ID   int64 `path:"id" doc:"Catalog engine id"`
	NSFW bool  `query:"nsfw" doc:"true/1 = count r18 works in work_count (default false = excluded)"`
}
type publicEngineOutput struct {
	Body Envelope[dto.PublicEngine]
}

// ── A2-1c release-calendar buckets ───────────────────────────────────────────
//
// The three buckets partition the works whose earliest dated release is known
// to a month (month bucket), to a year only (pending) or not at all (tba). They
// share the works-list paging posture, the works-list population predicate, the
// works-list item shape and its full include= vocabulary; what they add is the
// olang population gate and a bucket-level ETag.

type publicCalendarInput struct {
	Month   string `query:"month" doc:"ISO month YYYY-MM; default = the CURRENT Asia/Tokyo month, echoed back in the response. A malformed value is a 400"`
	OLang   string `query:"olang" doc:"Original-language gate: comma-separated olang values in the upstream BCP-47 spelling (ja, zh-Hans, en, …) or 'all' to switch it off. Default = the ja + zh* family. olang is an OPEN vocabulary, so an unrecognized value yields an empty bucket, never a 400"`
	Cursor  string `query:"cursor" doc:"Opaque keyset cursor from a prior next_cursor; omit for the first page"`
	Limit   int    `query:"limit" doc:"Items per page 1-100 (default 20); above 100 is clamped to 100, a non-positive or non-numeric value is a 400"`
	NSFW    bool   `query:"nsfw" doc:"true/1 = include r18 works (default false = dropped)"`
	Include string `query:"include" doc:"Comma-separated rich-brief blocks: names,intros,labels,ratings,covers,refs — the works-list vocabulary verbatim (unknown tokens ignored)"`
}
type publicCalendarOutput struct {
	Body Envelope[dto.PublicCalendarData]
}

type publicCalendarPendingInput struct {
	Year    string `query:"year" doc:"YYYY; default = the CURRENT Asia/Tokyo year, echoed back in the response. A malformed value is a 400"`
	OLang   string `query:"olang" doc:"Original-language gate: comma-separated olang values in the upstream BCP-47 spelling (ja, zh-Hans, en, …) or 'all' to switch it off. Default = the ja + zh* family. olang is an OPEN vocabulary, so an unrecognized value yields an empty bucket, never a 400"`
	Cursor  string `query:"cursor" doc:"Opaque keyset cursor from a prior next_cursor; omit for the first page"`
	Limit   int    `query:"limit" doc:"Items per page 1-100 (default 20); above 100 is clamped to 100, a non-positive or non-numeric value is a 400"`
	NSFW    bool   `query:"nsfw" doc:"true/1 = include r18 works (default false = dropped)"`
	Include string `query:"include" doc:"Comma-separated rich-brief blocks: names,intros,labels,ratings,covers,refs — the works-list vocabulary verbatim (unknown tokens ignored)"`
}
type publicCalendarPendingOutput struct {
	Body Envelope[dto.PublicCalendarData]
}

type publicCalendarTBAInput struct {
	OLang   string `query:"olang" doc:"Original-language gate: comma-separated olang values in the upstream BCP-47 spelling (ja, zh-Hans, en, …) or 'all' to switch it off. Default = the ja + zh* family. olang is an OPEN vocabulary, so an unrecognized value yields an empty bucket, never a 400"`
	Cursor  string `query:"cursor" doc:"Opaque keyset cursor from a prior next_cursor; omit for the first page"`
	Limit   int    `query:"limit" doc:"Items per page 1-100 (default 20); above 100 is clamped to 100, a non-positive or non-numeric value is a 400"`
	NSFW    bool   `query:"nsfw" doc:"true/1 = include r18 works (default false = dropped)"`
	Include string `query:"include" doc:"Comma-separated rich-brief blocks: names,intros,labels,ratings,covers,refs — the works-list vocabulary verbatim (unknown tokens ignored)"`
}
type publicCalendarTBAOutput struct {
	Body Envelope[dto.PublicCalendarData]
}

// SetupCatalogPublicSpec registers the /v1/catalog public projection operations
// to derive the frozen public OpenAPI. Handlers are stubs (Fiber serves the live
// paths); this only shapes the spec.
func SetupCatalogPublicSpec(app *fiber.App) huma.API {
	cfg := huma.DefaultConfig("NextMoe Open API — Catalog", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	api := humafiber.New(app, cfg)

	tags := []string{"catalog-public"}
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogWorkPublic", Method: http.MethodGet, Path: "/v1/catalog/works/{id}",
		Summary: "Frozen work record: identity + titles + exact cross-source refs + claim pointer; include=relations,credits", Tags: tags,
	}, func(context.Context, *publicWorkInput) (*publicWorkOutput, error) { return &publicWorkOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "lookupCatalogPublic", Method: http.MethodGet, Path: "/v1/catalog/lookup",
		Summary: "Reverse-lookup an external id via an EXACT anchor (killer feature); type=work|name|character|label, 404 on miss/hidden", Tags: tags,
	}, func(context.Context, *publicLookupInput) (*publicLookupOutput, error) {
		return &publicLookupOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "lookupCatalogBatchPublic", Method: http.MethodPost, Path: "/v1/catalog/lookup/batch",
		Summary: "Batch external-id reverse-lookup (≤100 pairs, per-pair type); misses return null blocks in order", Tags: tags,
	}, func(context.Context, *publicLookupBatchInput) (*publicLookupBatchOutput, error) {
		return &publicLookupBatchOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "resolveCatalogPublic", Method: http.MethodPost, Path: "/v1/catalog/resolve",
		Summary: "Batch old id → canonical id (redirect flattening) for a given entity_type", Tags: tags,
	}, func(context.Context, *publicResolveInput) (*publicResolveOutput, error) {
		return &publicResolveOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogRedirectsPublic", Method: http.MethodGet, Path: "/v1/catalog/redirects",
		Summary: "Keyset feed of id-convergence (merge) events for stored-id cleanup; filter by entity_type", Tags: tags,
	}, func(context.Context, *publicRedirectsInput) (*publicRedirectsOutput, error) {
		return &publicRedirectsOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogNamePublic", Method: http.MethodGet, Path: "/v1/catalog/names/{id}",
		Summary: "Credited identity (same-person grouping via public links); include=credits attaches works + roles", Tags: tags,
	}, func(context.Context, *publicPersonInput) (*publicPersonOutput, error) {
		return &publicPersonOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogCharacterPublic", Method: http.MethodGet, Path: "/v1/catalog/characters/{id}",
		Summary: "Character identity; include=works attaches the works it appears in with voice names", Tags: tags,
	}, func(context.Context, *publicCharacterInput) (*publicCharacterOutput, error) {
		return &publicCharacterOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogLabelPublic", Method: http.MethodGet, Path: "/v1/catalog/labels/{id}",
		Summary: "Label (brand / circle / publisher …) identity; include=works attaches attributed works", Tags: tags,
	}, func(context.Context, *publicLabelInput) (*publicLabelOutput, error) { return &publicLabelOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "searchCatalogEntitiesPublic", Method: http.MethodGet, Path: "/v1/catalog/search",
		Summary: "Entity autocomplete over names / characters / labels / works / tags, projected to public briefs",
		Description: "The identity finder: up to 20 flat hits of ONE family, no filters and no pagination — what a picker or a " +
			"jump-to-entity box needs. For a works RESULTS PAGE (filters, facets, sort, paging, full works-list rows) use " +
			"GET /v1/catalog/works/search instead.",
		Tags: tags,
	}, func(context.Context, *publicEntitySearchInput) (*publicEntitySearchOutput, error) {
		return &publicEntitySearchOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "searchCatalogWorksPublic", Method: http.MethodGet, Path: "/v1/catalog/works/search",
		Summary: "Works product search: free text + the works-list filter set, page-paginated, with opt-in facets and five sort lanes",
		Description: "Searches the LIVE galgame registry (claimed + bodyless) by any indexed title or alias and narrows it with the " +
			"same filters GET /v1/catalog/works accepts. Items are works-list rows VERBATIM (PublicWorkListItem, include= and all), " +
			"re-hydrated from the registry — the search documents never reach the wire. " +
			"total, the facet distribution and items are three views of ONE filtered set: page through total and you collect exactly " +
			"that many rows, and an sfw caller's total already excludes the r18 works it can never receive.",
		Tags: tags,
	}, func(context.Context, *publicWorksSearchInput) (*publicWorksSearchOutput, error) {
		return &publicWorksSearchOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogWorksPublic", Method: http.MethodGet, Path: "/v1/catalog/works",
		Summary: "Keyset works browse lane: the LIVE galgame registry set (claimed + bodyless) with conjunctive filters; sort=id|updated", Tags: tags,
	}, func(context.Context, *publicWorksListInput) (*publicWorksListOutput, error) {
		return &publicWorksListOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogChangesPublic", Method: http.MethodGet, Path: "/v1/catalog/changes",
		Summary: "Incremental works changes feed ((updated,id) keyset; next_cursor always present — keep polling it for new rows)",
		Description: "Creations and updates of LIVE galgame works, ordered by (updated_at, id) ASC. " +
			"The feed deliberately trails real time by ~5 seconds: updated_at is statement time, not commit time, " +
			"so serving rows younger than that lag would let a slow transaction commit behind an already-advanced " +
			"consumer cursor and be skipped forever. " +
			"DELETIONS DO NOT FLOW THROUGH THIS FEED — a row that leaves the LIVE set simply stops appearing; " +
			"merge-style disappearances are covered by /v1/catalog/redirects, and mirror-style consumers should " +
			"periodically reconcile the full id set via works?sort=id.",
		Tags: tags,
	}, func(context.Context, *publicChangesInput) (*publicChangesOutput, error) {
		return &publicChangesOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogTagPublic", Method: http.MethodGet, Path: "/v1/catalog/tags/{id}",
		Summary: "Canonical tag (cross-source vocabulary): name / tier / kind / intros; include=works attaches the tagged works", Tags: tags,
	}, func(context.Context, *publicTagInput) (*publicTagOutput, error) { return &publicTagOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogLabelsPublic", Method: http.MethodGet, Path: "/v1/catalog/labels",
		Summary: "Keyset label browse lane (id ASC); filter by kind, each row carries an nsfw-aware work_count",
		Description: "Every label that has not been merged away, id ascending. " +
			"work_count is the number of works THIS caller would page through via works?label_id=<id> — " +
			"so an sfw caller's count excludes r18 works and always matches the list it can actually fetch.",
		Tags: tags,
	}, func(context.Context, *publicLabelsListInput) (*publicLabelsListOutput, error) {
		return &publicLabelsListOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogTagsPublic", Method: http.MethodGet, Path: "/v1/catalog/tags",
		Summary: "Keyset canonical-tag browse lane (id ASC); filter by tier / kind, each row carries an nsfw-aware work_count",
		Description: "The cross-source canonical tag vocabulary, id ascending. " +
			"work_count is the number of works THIS caller would page through via works?tag_id=<id> " +
			"(counted over the source-tag map, so a work carrying two mapped source tags counts once).",
		Tags: tags,
	}, func(context.Context, *publicTagsListInput) (*publicTagsListOutput, error) {
		return &publicTagsListOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogEnginesPublic", Method: http.MethodGet, Path: "/v1/catalog/engines",
		Summary: "Keyset engine browse lane (id ASC); each row carries an nsfw-aware work_count",
		Description: "The visual-novel / game engines works are built with, id ascending. " +
			"work_count is the number of works THIS caller would page through via works?engine_id=<id>.",
		Tags: tags,
	}, func(context.Context, *publicEnginesListInput) (*publicEnginesListOutput, error) {
		return &publicEnginesListOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getCatalogEnginePublic", Method: http.MethodGet, Path: "/v1/catalog/engines/{id}",
		Summary: "Engine record: name + nsfw-aware work_count + exact cross-source refs", Tags: tags,
	}, func(context.Context, *publicEngineInput) (*publicEngineOutput, error) {
		return &publicEngineOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogCalendarPublic", Method: http.MethodGet, Path: "/v1/catalog/calendar",
		Summary: "Release calendar, one ISO month (date ASC keyset); default = the current Asia/Tokyo month",
		Description: "Works whose EARLIEST year-carrying, non-deleted release falls inside the month — day precision and " +
			"month precision alike (a month-precision work sorts at the head of its month, it is never pinned to the 1st). " +
			"Same classification anchor as the works-list release_date, so a row's bucket and its printed date can never disagree. " +
			"Year-precision works live in /v1/catalog/calendar/pending, undated ones in /v1/catalog/calendar/tba, and a work with " +
			"no release row at all is in no bucket. Items are works-list rows verbatim, include= and all. " +
			"Carries a bucket-level ETag (count + newest updated_at over the whole filtered set): an If-None-Match hit 304s " +
			"before any page is loaded.",
		Tags: tags,
	}, func(context.Context, *publicCalendarInput) (*publicCalendarOutput, error) {
		return &publicCalendarOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogCalendarPendingPublic", Method: http.MethodGet, Path: "/v1/catalog/calendar/pending",
		Summary: "Release calendar, one year's month-still-unknown bucket (id ASC keyset); default = the current Asia/Tokyo year",
		Description: "Works whose earliest release is known only to the YEAR — they appear in no month view of that year, by design. " +
			"Same population, item shape, olang gate and ETag mechanics as the month bucket.",
		Tags: tags,
	}, func(context.Context, *publicCalendarPendingInput) (*publicCalendarPendingOutput, error) {
		return &publicCalendarPendingOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogCalendarTBAPublic", Method: http.MethodGet, Path: "/v1/catalog/calendar/tba",
		Summary: "Release calendar, the global announced-but-undated bucket (id ASC keyset)",
		Description: "Works that HAVE release rows but none carrying a year. A work with no release row at all is 'unknown' " +
			"and deliberately enters no bucket — absence of a release is absence of an announcement, not a TBA date.",
		Tags: tags,
	}, func(context.Context, *publicCalendarTBAInput) (*publicCalendarTBAOutput, error) {
		return &publicCalendarTBAOutput{}, nil
	})
	return api
}
