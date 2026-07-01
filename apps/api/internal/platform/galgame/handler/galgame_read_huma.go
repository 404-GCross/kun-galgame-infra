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

type batchInput struct {
	IDs          string `query:"ids" doc:"Comma-separated galgame IDs (1-100)"`
	ContentLimit string `query:"content_limit" doc:"sfw | nsfw | all (default sfw)"`
	View         string `query:"view" enum:"brief,detail" doc:"brief (default) = GalgameBrief; detail = GalgameDetailBrief (adds release_date / intro_* / officials)"`
}

type batchOutput struct {
	Body calEnvelope[[]dto.GalgameDetailBrief]
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
		OperationID: "batchGetGalgames", Method: http.MethodGet, Path: "/api/galgame/batch",
		Summary: "Batch galgame briefs (view=brief default; view=detail adds intro/officials/release)", Tags: tags,
	}, func(context.Context, *batchInput) (*batchOutput, error) { return &batchOutput{}, nil })
	return api
}
