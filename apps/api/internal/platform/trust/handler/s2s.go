package handler

import (
	"context"
	stderrors "errors"
	"log/slog"
	"net/http"

	"api/internal/platform/trust/dto"
	"api/internal/platform/trust/service"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// Server holds the S2S intake-face dependencies.
type Server struct {
	reports  *service.ReportService
	registry *service.RegistryService
}

// Setup builds the trust S2S Huma API over the Fiber app. S2SAuth is applied by
// the caller as path-scoped Fiber middleware BEFORE this. Callable with nil
// services for spec export (handlers are never invoked then).
func Setup(app *fiber.App, reports *service.ReportService, registry *service.RegistryService) huma.API {
	InstallErrorEnvelope()

	cfg := huma.DefaultConfig("KUN Trust Service", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""

	api := humafiber.New(app, cfg)
	api.UseMiddleware(S2SBridge)

	s := &Server{reports: reports, registry: registry}
	s.register(api)
	return api
}

func (s *Server) register(api huma.API) {
	intake := []string{"trust-intake"}
	huma.Register(api, huma.Operation{OperationID: "submitReport", Method: http.MethodPost, Path: "/api/v1/trust/reports",
		Summary: "Submit a report on a subject (dedup / rate-limit / weight / aggregate)", Tags: intake}, s.submitReport)
	huma.Register(api, huma.Operation{OperationID: "listSubjectKinds", Method: http.MethodGet, Path: "/api/v1/trust/subject-kinds",
		Summary: "List the calling site's registered subject kinds", Tags: intake}, s.listSubjectKinds)
}

type submitReportInput struct{ Body dto.ReportRequest }
type submitReportOutput struct {
	Body Envelope[dto.ReportResponse]
}

func (s *Server) submitReport(ctx context.Context, in *submitReportInput) (*submitReportOutput, error) {
	site, he := siteBinding(ctx)
	if he != nil {
		return nil, he
	}
	res, err := s.reports.Submit(ctx, service.ReportParams{
		Site: site, SubjectKind: in.Body.SubjectKind, SubjectID: in.Body.SubjectID,
		ReasonKey: in.Body.ReasonKey, Note: in.Body.Note, Snapshot: in.Body.Snapshot,
		ReporterID: in.Body.ReporterID,
	})
	if err != nil {
		return nil, mapIntakeErr("submit report", err)
	}
	return &submitReportOutput{Body: okEnvelope(dto.ReportResponse{
		ReportID: res.ReportID, ReviewItemID: res.ReviewItemID,
	})}, nil
}

type listSubjectKindsOutput struct {
	Body Envelope[dto.SubjectKindsResponse]
}

func (s *Server) listSubjectKinds(ctx context.Context, _ *struct{}) (*listSubjectKindsOutput, error) {
	site, he := siteBinding(ctx)
	if he != nil {
		return nil, he
	}
	kinds, err := s.registry.ListSubjectKinds(ctx, site, false)
	if err != nil {
		return nil, mapIntakeErr("list subject kinds", err)
	}
	return &listSubjectKindsOutput{Body: okEnvelope(dto.SubjectKindsResponse{
		Kinds: toSubjectKindViews(kinds),
	})}, nil
}

// mapIntakeErr translates an intake service error into the house envelope.
func mapIntakeErr(op string, err error) *houseError {
	switch {
	case stderrors.Is(err, service.ErrSubjectKindNotRegistered):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed,
			"subject_kind is not registered for this site")
	case stderrors.Is(err, service.ErrReasonUnknown):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed,
			"unknown report reason for this site")
	case stderrors.Is(err, service.ErrRateLimited):
		return apiErrMsg(http.StatusTooManyRequests, errors.ErrOperationFailed,
			"reporter rate limit exceeded; try again later")
	default:
		slog.Error("trust "+op, "err", err)
		return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
}
