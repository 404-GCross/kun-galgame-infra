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

type draftsInput struct {
	Page         int    `query:"page" doc:"Page number (default 1)"`
	Limit        int    `query:"limit" doc:"Items per page 1-50 (default 24)"`
	ContentLimit string `query:"content_limit" doc:"sfw | nsfw | all (default sfw)"`
	OfficialID   int    `query:"official_id" doc:"Scope to one official's drafts (0/absent = global). Drafts carry VNDB-synced official edges."`
	TagID        int    `query:"tag_id" doc:"Scope to one tag's drafts (0/absent = global). Drafts carry VNDB-synced tag edges."`
	EngineID     int    `query:"engine_id" doc:"Scope to one engine's drafts (0/absent = global). Empty by data today: engine edges are human-curated and drafts are untouched VNDB imports."`
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

type revisionsInput struct {
	GID          int  `path:"gid" doc:"Galgame ID"`
	Page         int  `query:"page" doc:"Page number (default 1)"`
	Limit        int  `query:"limit" doc:"Items per page (default 20)"`
	IncludeMinor bool `query:"include_minor" doc:"Include minor (enrichment) revisions"`
}
type revisionsOutput struct {
	Body calEnvelope[dto.RevisionListData]
}

type revisionInput struct {
	GID int `path:"gid" doc:"Galgame ID"`
	Rev int `path:"rev" doc:"Revision number"`
}
type revisionOutput struct {
	Body calEnvelope[dto.RevisionResponse]
}
type revisionDiffOutput struct {
	Body calEnvelope[dto.RevisionDiffData]
}

type prsInput struct {
	GID   int `path:"gid" doc:"Galgame ID"`
	Page  int `query:"page" doc:"Page number (default 1)"`
	Limit int `query:"limit" doc:"Items per page"`
}
type prsOutput struct {
	Body calEnvelope[dto.PRListData]
}

type prInput struct {
	GID int `path:"gid" doc:"Galgame ID"`
	ID  int `path:"id" doc:"PR ID"`
}
type prOutput struct {
	Body calEnvelope[dto.PRDetailData]
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

type recentRevisionsInput struct {
	SinceID int64 `query:"since_id" doc:"Cursor: return events with id > since_id"`
	Limit   int   `query:"limit" doc:"Max items (1-5000)"`
}
type recentRevisionsOutput struct {
	Body calEnvelope[dto.RevisionFeedResponse]
}

type recentTaxonomyInput struct {
	Entity  string `query:"entity" enum:"tag,official,engine,series" doc:"Filter by entity kind"`
	Action  string `query:"action" enum:"created,updated,deleted,reverted" doc:"Filter by action"`
	SinceID int64  `query:"since_id" doc:"Cursor: return events with id > since_id"`
	Limit   int    `query:"limit" doc:"Max items (1-5000)"`
}
type recentTaxonomyOutput struct {
	Body calEnvelope[dto.TaxonomyFeedResponse]
}

type messagesFeedInput struct {
	SinceID int64 `query:"since_id" doc:"Cursor: return messages with id > since_id"`
	Limit   int   `query:"limit" doc:"Max items"`
}
type messagesFeedOutput struct {
	Body calEnvelope[dto.MessageFeedResponse]
}

type messagesMineInput struct {
	SinceID int64 `query:"since_id" doc:"Cursor: return messages with id > since_id"`
	Limit   int   `query:"limit" doc:"Max items (1-100)"`
}
type messagesMineOutput struct {
	Body calEnvelope[dto.MessageMineData]
}

type mineInput struct {
	Status string `query:"status" doc:"Filter by submission status"`
	Page   int    `query:"page" doc:"Page number (default 1)"`
	Limit  int    `query:"limit" doc:"Items per page (1-50)"`
}
type mineOutput struct {
	Body calEnvelope[dto.MineGalgameListData]
}

// entityGalgamesInput is the shared input for the taxonomy reverse-lookup reads
// (official/tag → galgames) — offset-paginated, SFW-gated (step 20).
type entityGalgamesInput struct {
	ID           int    `path:"id" doc:"Official / Tag ID"`
	Page         int    `query:"page" doc:"Page number (default 1)"`
	Limit        int    `query:"limit" doc:"Items per page 1-50 (default 24)"`
	SortField    string `query:"sort_field" enum:"created,resource_update_time,view" doc:"Sort field (default resource_update_time)"`
	SortOrder    string `query:"sort_order" enum:"asc,desc" doc:"Sort direction (default desc)"`
	ContentLimit string `query:"content_limit" doc:"sfw | nsfw | all (default sfw)"`
}
type officialGalgamesOutput struct {
	Body calEnvelope[dto.OfficialGalgamesResponse]
}
type tagGalgamesOutput struct {
	Body calEnvelope[dto.TagGalgamesResponse]
}

// scores / stats — the cross-source read face (step 34). Served by the Fiber
// handlers in scores_handler.go; spec-only here so the OpenAPI derives from the
// same dto.* types.
type scoresOutput struct {
	Body calEnvelope[dto.GalgameScores]
}
type statsInput struct{}
type statsOutput struct {
	Body calEnvelope[dto.GalgameStatsData]
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
		OperationID: "listGalgameDrafts", Method: http.MethodGet, Path: "/api/galgame/drafts",
		Summary: "Paginated unclaimed VNDB drafts (status=2), newest first — the claim-funnel browser; same envelope as the list", Tags: tags,
	}, func(context.Context, *draftsInput) (*listOutput, error) { return &listOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameDetail", Method: http.MethodGet, Path: "/api/galgame/{gid}",
		Summary: "Full galgame detail (all relations) + owner/contributor user briefs", Tags: tags,
	}, func(context.Context, *detailInput) (*detailOutput, error) { return &detailOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "listUserGalgames", Method: http.MethodGet, Path: "/api/galgame/user/{id}/galgames",
		Summary: "A user's published galgames (briefs, paginated) — profile 已发布 tab", Tags: tags,
	}, func(context.Context, *userGalgamesInput) (*userGalgamesOutput, error) {
		return &userGalgamesOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listUserContributedGalgames", Method: http.MethodGet, Path: "/api/galgame/user/{id}/contributed",
		Summary: "Galgames a user contributed to (briefs, paginated) — profile 参与编辑 tab", Tags: tags,
	}, func(context.Context, *userGalgamesInput) (*userGalgamesOutput, error) {
		return &userGalgamesOutput{}, nil
	})
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
	huma.Register(api, huma.Operation{
		OperationID: "listGalgameRevisions", Method: http.MethodGet, Path: "/api/galgame/{gid}/revisions",
		Summary: "A galgame's edit history (revisions, paginated)", Tags: tags,
	}, func(context.Context, *revisionsInput) (*revisionsOutput, error) { return &revisionsOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameRevision", Method: http.MethodGet, Path: "/api/galgame/{gid}/revisions/{rev}",
		Summary: "One revision (with its full snapshot)", Tags: tags,
	}, func(context.Context, *revisionInput) (*revisionOutput, error) { return &revisionOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameRevisionDiff", Method: http.MethodGet, Path: "/api/galgame/{gid}/revisions/{rev}/diff",
		Summary: "A revision's diff vs its predecessor (changed keys + old/new snapshot + entity names)", Tags: tags,
	}, func(context.Context, *revisionInput) (*revisionDiffOutput, error) { return &revisionDiffOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "listGalgamePRs", Method: http.MethodGet, Path: "/api/galgame/{gid}/prs",
		Summary: "A galgame's pull requests (edit proposals, paginated)", Tags: tags,
	}, func(context.Context, *prsInput) (*prsOutput, error) { return &prsOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgamePR", Method: http.MethodGet, Path: "/api/galgame/{gid}/prs/{id}",
		Summary: "One PR with its diff vs the base revision", Tags: tags,
	}, func(context.Context, *prInput) (*prOutput, error) { return &prOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getRecentGalgameRevisions", Method: http.MethodGet, Path: "/api/galgame/revisions/recent",
		Summary: "Recent merged galgame edit (revision) events — S2S activity feed", Tags: tags,
	}, func(context.Context, *recentRevisionsInput) (*recentRevisionsOutput, error) {
		return &recentRevisionsOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getRecentTaxonomyRevisions", Method: http.MethodGet, Path: "/api/galgame/taxonomy/recent",
		Summary: "Recent taxonomy (tag/official/engine/series) change events — S2S activity feed", Tags: tags,
	}, func(context.Context, *recentTaxonomyInput) (*recentTaxonomyOutput, error) {
		return &recentTaxonomyOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getMyGalgameSubmissions", Method: http.MethodGet, Path: "/api/galgame/mine",
		Summary: "The viewer's own galgame submissions (all statuses)", Tags: tags,
	}, func(context.Context, *mineInput) (*mineOutput, error) { return &mineOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameMessagesFeed", Method: http.MethodGet, Path: "/api/galgame/messages/feed",
		Summary: "Galgame notification feed (S2S; kungal/moyu cron pulls these)", Tags: tags,
	}, func(context.Context, *messagesFeedInput) (*messagesFeedOutput, error) {
		return &messagesFeedOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getMyGalgameMessages", Method: http.MethodGet, Path: "/api/galgame/messages/mine",
		Summary: "The viewer's own galgame notifications", Tags: tags,
	}, func(context.Context, *messagesMineInput) (*messagesMineOutput, error) {
		return &messagesMineOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listOfficialGalgames", Method: http.MethodGet, Path: "/api/galgame/officials/{id}/galgames",
		Summary: "An official's self-description (name/aliases/category) + a page of the galgames under it — the circle/brand reverse face (page-direct self-sufficient)", Tags: tags,
	}, func(context.Context, *entityGalgamesInput) (*officialGalgamesOutput, error) {
		return &officialGalgamesOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "listTagGalgames", Method: http.MethodGet, Path: "/api/galgame/tags/{id}/galgames",
		Summary: "A tag's self-description (name/aliases/category) + a page of the galgames carrying it (page-direct self-sufficient)", Tags: tags,
	}, func(context.Context, *entityGalgamesInput) (*tagGalgamesOutput, error) {
		return &tagGalgamesOutput{}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameScores", Method: http.MethodGet, Path: "/api/galgame/{gid}/scores",
		Summary: "Three-source (VNDB / Bangumi / EG) rating snapshot for one galgame — each source carries a backend-composed attribution URL; a missing source is null", Tags: tags,
	}, func(context.Context, *gidInput) (*scoresOutput, error) { return &scoresOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameStats", Method: http.MethodGet, Path: "/api/galgame/stats",
		Summary: "Cross-source galgame statistics overview (release-year distribution / per-source score histograms / yearly averages / coverage) — verbatim snapshot payloads, ETag-cached", Tags: tags,
	}, func(context.Context, *statsInput) (*statsOutput, error) { return &statsOutput{}, nil })
	registerGalgameSearchOps(api, tags)
	return api
}
