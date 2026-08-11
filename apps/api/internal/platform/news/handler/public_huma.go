package handler

import (
	"context"
	"net/http"

	"api/internal/platform/news/dto"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

type newsListInput struct {
	Source          string `query:"source" doc:"Comma-separated source keys to keep (ymgal, galgame_hihyou); omit or 'all' for every source"`
	WorkID          int64  `query:"work_id" doc:"Keep only items anchored to this catalog work id"`
	PublishedAfter  string `query:"published_after" doc:"RFC3339 lower bound on the UPSTREAM publication time"`
	PublishedBefore string `query:"published_before" doc:"RFC3339 upper bound on the UPSTREAM publication time"`
	Cursor          string `query:"cursor" doc:"Opaque keyset cursor from a prior response's next_cursor; omit for the first page"`
	Limit           int    `query:"limit" doc:"Items per page 1-50 (default 20)"`
}
type newsListOutput struct {
	Body Envelope[dto.PublicNewsFeedData]
}

type newsItemInput struct {
	ID int64 `path:"id" doc:"News item id"`
}
type newsItemOutput struct {
	Body Envelope[dto.PublicNewsItem]
}

type newsSourcesOutput struct {
	Body Envelope[dto.PublicNewsSourcesData]
}

// SetupNewsPublicSpec describes the /v1/news face. Handlers are stubs: the face
// is served by the fiber handlers in this package, and this exists to export the
// contract (cmd/gen-openapi -news-public → docs/news/public-openapi.yaml).
func SetupNewsPublicSpec(app *fiber.App) huma.API {
	InstallErrorEnvelope()

	cfg := huma.DefaultConfig("NextMoe Open API — News", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	api := humafiber.New(app, cfg)

	tags := []string{"news-public"}
	huma.Register(api, huma.Operation{
		OperationID: "listNewsPublic", Method: http.MethodGet, Path: "/v1/news",
		Summary: "Galgame news feed republished from partner sites, newest upstream publication first",
		Description: "Every item carries its source block and source_url unconditionally: the partners authorised an INDEX, " +
			"not a mirror. The article body is never served here and is not stored — preview plus banner is the whole " +
			"authorisation, and readers reach the full text by following source_url to the partner's own site. " +
			"Items we withdrew, and items whose upstream original has disappeared, are absent from this feed.",
		Tags: tags,
	}, func(context.Context, *newsListInput) (*newsListOutput, error) { return &newsListOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getNewsPublic", Method: http.MethodGet, Path: "/v1/news/{id}",
		Summary:     "One news item; 404 once it is unpublished, withdrawn, or gone upstream",
		Description: "The 404 is a contract, not a lookup failure: a withdrawn item must stop being addressable.",
		Tags:        tags,
	}, func(context.Context, *newsItemInput) (*newsItemOutput, error) { return &newsItemOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "listNewsSourcesPublic", Method: http.MethodGet, Path: "/v1/news/sources",
		Summary: "The source registry: display name, homepage, column entry point, publisher uid, and the attribution text to render",
		Description: "For pages that render one standing attribution block. It does NOT replace the per-item source block — " +
			"an item taken on its own must still carry its own attribution.",
		Tags: tags,
	}, func(context.Context, *struct{}) (*newsSourcesOutput, error) { return &newsSourcesOutput{}, nil })

	return api
}
