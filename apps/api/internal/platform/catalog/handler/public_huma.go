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
	ID      int64  `path:"id" doc:"Catalog work id"`
	Include string `query:"include" doc:"Comma-separated heavy blocks: relations,credits (default: none)"`
	NSFW    bool   `query:"nsfw" doc:"true/1 = serve r18 works and r18 relation ends (caller-controlled; default false = hidden)"`
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
	Type   string `query:"type" enum:"names,characters,labels,works" doc:"Which index to search; works (wave 105) searches every LIVE galgame registry work by any title"`
	Q      string `query:"q" doc:"Search text; empty returns the most-credited entities"`
	Locale string `query:"locale" enum:"zh,ja,en" doc:"UI locale; the server pins the query language"`
	Limit  int    `query:"limit" doc:"Max hits (capped at 20)"`
	NSFW   bool   `query:"nsfw" doc:"works only: true/1 = include r18 hits (default false = excluded server-side)"`
}
type publicEntitySearchOutput struct {
	Body Envelope[dto.PublicEntitySearchData]
}

type publicWorksListInput struct {
	ContentRating  string `query:"content_rating" enum:"all_ages,sensitive,r18" doc:"Filter by rating (r18 additionally requires nsfw=1)"`
	Claimed        string `query:"claimed" enum:"true,false" doc:"true = claimed works only; false = bodyless only; absent = both"`
	LabelID        int64  `query:"label_id" doc:"Only works attributed to this label (the catalog_work_label edge)"`
	TagID          int64  `query:"tag_id" doc:"Only works carrying a source tag mapped to this canonical tag"`
	SeriesID       int64  `query:"series_id" doc:"Only member works of this series"`
	Platform       string `query:"platform" doc:"vndb platform code (win/and/ios/...) — release-level and work-level rows unioned"`
	ReleasedAfter  string `query:"released_after" doc:"YYYY-MM-DD, inclusive, over the EARLIEST release date per work"`
	ReleasedBefore string `query:"released_before" doc:"YYYY-MM-DD, inclusive"`
	IDs            string `query:"ids" doc:"Comma-separated work ids (max 100) — the batch-hydrate lane"`
	Sort           string `query:"sort" enum:"id,updated" doc:"id = ascending browse order (default); updated = newest-updated first"`
	Cursor         string `query:"cursor" doc:"Opaque keyset cursor from a prior next_cursor; omit for the first page"`
	Limit          int    `query:"limit" doc:"Items per page 1-100 (default 20); above 100 is clamped to 100, a non-positive or non-numeric value is a 400"`
	NSFW           bool   `query:"nsfw" doc:"true/1 = include r18 works (default false = dropped)"`
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
		Summary: "Entity relevance search over names / characters / labels, projected to public briefs", Tags: tags,
	}, func(context.Context, *publicEntitySearchInput) (*publicEntitySearchOutput, error) {
		return &publicEntitySearchOutput{}, nil
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
		Summary: "Canonical tag (cross-source vocabulary): name / tier / kind; include=works attaches the tagged works", Tags: tags,
	}, func(context.Context, *publicTagInput) (*publicTagOutput, error) { return &publicTagOutput{}, nil })
	return api
}
