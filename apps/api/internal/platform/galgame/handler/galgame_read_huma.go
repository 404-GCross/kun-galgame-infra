// Galgame-wiki read-endpoint OpenAPI (code-first) — SPEC ONLY.
//
// Like calendar_huma.go, these read endpoints are SERVED by the existing Fiber
// handlers (they're viewer-aware / high-traffic; keeping the tested serving is
// lower risk than rewiring). This file registers them on a Huma API purely so
// cmd/gen-openapi can derive the OpenAPI from the SAME dto.* types the handlers
// already return — so the spec + generated TypeScript can't drift. Never mounted
// on the live service.
//
// batch: the /galgame/batch response reuses the existing dto.GalgameBrief /
// GalgameDetailBrief (already returned verbatim by the handler). The spec models
// the richer view=detail shape (GalgameDetailBrief embeds GalgameBrief); the
// default view=brief returns only the GalgameBrief subset.
package handler

import (
	"context"
	"net/http"

	"api/internal/platform/galgame/dto"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

type listInput struct {
	Page           int    `query:"page" doc:"Page number (default 1)"`
	Limit          int    `query:"limit" doc:"Items per page 1-50 (default 24)"`
	SortField      string `query:"sort_field" enum:"created,updated,view,resource_update_time,release_date" doc:"Sort field (default created)"`
	SortOrder      string `query:"sort_order" enum:"asc,desc" doc:"Sort direction (default desc)"`
	Search         string `query:"search" doc:"Keyword across the four localized names"`
	ContentLimit   string `query:"content_limit" doc:"sfw | nsfw | all (default sfw)"`
	ReleasedFrom   string `query:"released_from" doc:"Release lower bound, YYYY or YYYY-MM"`
	ReleasedTo     string `query:"released_to" doc:"Release upper bound, YYYY or YYYY-MM"`
	ReleasedMonths string `query:"released_months" doc:"CSV of discontinuous months 1-12, AND'd on the year range"`
}

type listOutput struct {
	Body calEnvelope[dto.GalgameListData]
}

type batchInput struct {
	IDs          string `query:"ids" doc:"Comma-separated galgame IDs (1-100)"`
	ContentLimit string `query:"content_limit" doc:"sfw | nsfw | all (default sfw)"`
	View         string `query:"view" enum:"brief,detail" doc:"brief (default) = GalgameBrief; detail = GalgameDetailBrief (adds release_date / intro_* / officials)"`
}

type batchOutput struct {
	Body calEnvelope[[]dto.GalgameDetailBrief]
}

type detailInput struct {
	GID          int    `path:"gid" doc:"Galgame ID"`
	ContentLimit string `query:"content_limit" doc:"sfw | nsfw | all (default: no filter)"`
}

// galgameDetailResponse mirrors the {galgame, users} body of GET /galgame/:gid.
// users is keyed by user id (JSON object keys are strings) — the owner +
// contributor briefs, so a consumer renders names/avatars without extra lookups.
type galgameDetailResponse struct {
	Galgame dto.GalgameDetail        `json:"galgame"`
	Users   map[string]dto.UserBrief `json:"users"`
}

type detailOutput struct {
	Body calEnvelope[galgameDetailResponse]
}

type userGalgamesInput struct {
	ID           int    `path:"id" doc:"User ID"`
	Page         int    `query:"page" doc:"Page number (default 1)"`
	Limit        int    `query:"limit" doc:"Items per page 1-100 (default 24)"`
	ContentLimit string `query:"content_limit" doc:"sfw | nsfw | all (default sfw)"`
}
type userGalgamesOutput struct {
	Body calEnvelope[dto.UserGalgameListData]
}

type userStatsInput struct {
	ID int `path:"id" doc:"User ID"`
}
type userStatsOutput struct {
	Body calEnvelope[dto.UserGalgameStats]
}

// gidInput is the bare {gid} path input shared by the relation-list reads.
type gidInput struct {
	GID int `path:"gid" doc:"Galgame ID"`
}
type linksOutput struct {
	Body calEnvelope[[]dto.DetailLink]
}
type aliasesOutput struct {
	Body calEnvelope[[]dto.DetailAlias]
}
type contributorsOutput struct {
	Body calEnvelope[[]dto.GalgameContributorWithUser]
}

type checkVNDBInput struct {
	VNDBID string `query:"vndb_id" doc:"VNDB id to check (e.g. v12345)"`
}
type checkVNDBOutput struct {
	Body calEnvelope[dto.CheckVNDBResult]
}

// SetupGalgameReadSpec registers the galgame-wiki read operations to derive their
// spec. Handlers are stubs (never invoked — Fiber serves these paths).
func SetupGalgameReadSpec(app *fiber.App) huma.API {
	cfg := huma.DefaultConfig("KUN Galgame Wiki — Reads", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	api := humafiber.New(app, cfg)

	tags := []string{"galgame-read"}
	huma.Register(api, huma.Operation{
		OperationID: "listGalgames", Method: http.MethodGet, Path: "/api/galgame",
		Summary: "Paginated galgame list (search + sort + release-date filters); items are the full galgame shape with the list's preload subset populated", Tags: tags,
	}, func(context.Context, *listInput) (*listOutput, error) { return &listOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "batchGetGalgames", Method: http.MethodGet, Path: "/api/galgame/batch",
		Summary: "Batch galgame briefs (view=brief default; view=detail adds intro/officials/release)", Tags: tags,
	}, func(context.Context, *batchInput) (*batchOutput, error) { return &batchOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameDetail", Method: http.MethodGet, Path: "/api/galgame/{gid}",
		Summary: "Full galgame detail (all relations) + owner/contributor user briefs", Tags: tags,
	}, func(context.Context, *detailInput) (*detailOutput, error) { return &detailOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "listUserGalgames", Method: http.MethodGet, Path: "/api/galgame/user/{id}/galgames",
		Summary: "A user's published galgames (briefs, paginated) — profile 已发布 tab", Tags: tags,
	}, func(context.Context, *userGalgamesInput) (*userGalgamesOutput, error) { return &userGalgamesOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "listUserContributedGalgames", Method: http.MethodGet, Path: "/api/galgame/user/{id}/contributed",
		Summary: "Galgames a user contributed to (briefs, paginated) — profile 参与编辑 tab", Tags: tags,
	}, func(context.Context, *userGalgamesInput) (*userGalgamesOutput, error) { return &userGalgamesOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getUserGalgameStats", Method: http.MethodGet, Path: "/api/galgame/user/{id}/stats",
		Summary: "A user's aggregate galgame contribution stats", Tags: tags,
	}, func(context.Context, *userStatsInput) (*userStatsOutput, error) { return &userStatsOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameLinks", Method: http.MethodGet, Path: "/api/galgame/{gid}/links",
		Summary: "A galgame's external links (official sites, stores, socials)", Tags: tags,
	}, func(context.Context, *gidInput) (*linksOutput, error) { return &linksOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameAliases", Method: http.MethodGet, Path: "/api/galgame/{gid}/aliases",
		Summary: "A galgame's alternative names", Tags: tags,
	}, func(context.Context, *gidInput) (*aliasesOutput, error) { return &aliasesOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameContributors", Method: http.MethodGet, Path: "/api/galgame/{gid}/contributors",
		Summary: "A galgame's contributors, each with the resolved user brief", Tags: tags,
	}, func(context.Context, *gidInput) (*contributorsOutput, error) { return &contributorsOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "checkGalgameVNDB", Method: http.MethodGet, Path: "/api/galgame/check",
		Summary: "Whether a vndb_id already exists (and which galgame holds it)", Tags: tags,
	}, func(context.Context, *checkVNDBInput) (*checkVNDBOutput, error) { return &checkVNDBOutput{}, nil })
	registerGalgameSearchOps(api, tags)
	return api
}
