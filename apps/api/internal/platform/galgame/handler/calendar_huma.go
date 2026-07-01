// Release-calendar OpenAPI (code-first) — SPEC ONLY.
//
// The calendar is SERVED by the Fiber handlers in calendar_handler.go, which keep
// the ETag / If-None-Match 304 + Cache-Control conditional caching that Huma's
// typed request/response model doesn't fit. This file registers the same three
// read operations on a Huma API purely so cmd/gen-openapi can DERIVE the OpenAPI
// spec from Go types. The response bodies reuse the exact same dto.Calendar*
// types the Fiber handlers emit (pinned wire-identical to model.Galgame by
// calendar_dto_test.go), so the spec — and the TypeScript generated from it —
// cannot drift from what the endpoints actually return. SetupCalendarSpec is only
// ever called by gen-openapi on a throwaway app; it is never mounted on the live
// galgame service.
package handler

import (
	"context"
	"net/http"

	"api/internal/platform/galgame/dto"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// calEnvelope is the house response body {code,message,data} (matches
// pkg/response.Response) so the generated spec/TS reflect the real wire wrapper.
type calEnvelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type calMonthInput struct {
	Month            string `query:"month" doc:"ISO month YYYY-MM (default: current JST month)"`
	ContentLimit     string `query:"content_limit" doc:"sfw | nsfw | all (default sfw)"`
	OriginalLanguage string `query:"original_language" doc:"CSV of original languages; 'all' = no filter (default ja-jp,zh-cn,zh-tw)"`
}
type calMonthOutput struct {
	Body calEnvelope[dto.CalendarMonthData]
}

type calPendingInput struct {
	Year             string `query:"year" doc:"Year YYYY (default: current JST year)"`
	ContentLimit     string `query:"content_limit" doc:"sfw | nsfw | all (default sfw)"`
	OriginalLanguage string `query:"original_language" doc:"CSV of original languages; 'all' = no filter"`
}
type calPendingOutput struct {
	Body calEnvelope[dto.CalendarPendingData]
}

type calTBAInput struct {
	ContentLimit     string `query:"content_limit" doc:"sfw | nsfw | all (default sfw)"`
	OriginalLanguage string `query:"original_language" doc:"CSV of original languages; 'all' = no filter"`
}
type calTBAOutput struct {
	Body calEnvelope[dto.CalendarTBAData]
}

// SetupCalendarSpec registers the calendar read operations to derive their spec.
// The handlers are stubs (never invoked — the Fiber layer serves these paths).
func SetupCalendarSpec(app *fiber.App) huma.API {
	cfg := huma.DefaultConfig("KUN Galgame Wiki — Release Calendar", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	api := humafiber.New(app, cfg)

	tags := []string{"galgame-calendar"}
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameCalendarMonth", Method: http.MethodGet, Path: "/api/galgame/calendar",
		Summary: "Release calendar for one ISO month (day + month precision, mixed released/upcoming)", Tags: tags,
	}, func(context.Context, *calMonthInput) (*calMonthOutput, error) { return &calMonthOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameCalendarPending", Method: http.MethodGet, Path: "/api/galgame/calendar/pending",
		Summary: "Year-known, month-undecided bucket for a year", Tags: tags,
	}, func(context.Context, *calPendingInput) (*calPendingOutput, error) { return &calPendingOutput{}, nil })
	huma.Register(api, huma.Operation{
		OperationID: "getGalgameCalendarTBA", Method: http.MethodGet, Path: "/api/galgame/calendar/tba",
		Summary: "Global release-date-TBA bucket", Tags: tags,
	}, func(context.Context, *calTBAInput) (*calTBAOutput, error) { return &calTBAOutput{}, nil })
	return api
}
