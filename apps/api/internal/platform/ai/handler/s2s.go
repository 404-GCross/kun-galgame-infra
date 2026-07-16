package handler

import (
	"context"
	"log/slog"
	"net/http"

	"api/internal/platform/ai/dto"
	"api/internal/platform/ai/service"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// Server holds the S2S face dependencies.
type Server struct {
	moderation *service.ModerationService
}

// Setup builds the AI-gateway S2S Huma API over the Fiber app. S2SAuth is
// applied by the caller as path-scoped Fiber middleware BEFORE this. Callable
// with a nil service for spec export (handlers are never invoked then).
func Setup(app *fiber.App, moderation *service.ModerationService) huma.API {
	InstallErrorEnvelope()

	cfg := huma.DefaultConfig("KUN AI Gateway", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""

	api := humafiber.New(app, cfg)
	api.UseMiddleware(S2SBridge)

	s := &Server{moderation: moderation}
	s.register(api)
	return api
}

func (s *Server) register(api huma.API) {
	tags := []string{"ai"}
	huma.Register(api, huma.Operation{
		OperationID: "moderateText", Method: http.MethodPost, Path: "/api/v1/ai/moderate-text",
		Summary: "Scan a text for policy violations (async, fail-open; tenant derived from the client binding)",
		Tags:    tags,
	}, s.moderateText)
}

type moderateTextInput struct{ Body dto.ModerateTextRequest }
type moderateTextOutput struct {
	Body Envelope[dto.ModerateTextResponse]
}

func (s *Server) moderateText(ctx context.Context, in *moderateTextInput) (*moderateTextOutput, error) {
	site, he := siteBinding(ctx)
	if he != nil {
		return nil, he
	}
	res, err := s.moderation.Moderate(ctx, service.ModerateParams{
		Site: site, Text: in.Body.Text, AuthorID: in.Body.AuthorID,
	})
	if err != nil {
		// moderate-text is fail-open, so the service returns nil error on every
		// degraded path; a non-nil error here is an unexpected internal fault.
		slog.Error("ai moderate-text", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	return &moderateTextOutput{Body: okEnvelope(dto.ModerateTextResponse{
		Route:      res.Route,
		Flagged:    res.Flagged,
		Categories: res.Categories,
		Score:      res.Score,
		Channel:    res.Channel,
		Degraded:   res.Degraded,
	})}, nil
}
