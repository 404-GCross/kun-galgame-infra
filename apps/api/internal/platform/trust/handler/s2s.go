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
	forward  *service.ForwardService
	scan     *service.ScanService
	terms    *service.TermService
}

// Setup builds the trust S2S Huma API over the Fiber app. S2SAuth is applied by
// the caller as path-scoped Fiber middleware BEFORE this. Callable with nil
// services for spec export (handlers are never invoked then).
func Setup(app *fiber.App, reports *service.ReportService, registry *service.RegistryService, forward *service.ForwardService, scan *service.ScanService, terms *service.TermService) huma.API {
	InstallErrorEnvelope()

	cfg := huma.DefaultConfig("KUN Trust Service", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""

	api := humafiber.New(app, cfg)
	api.UseMiddleware(S2SBridge)

	s := &Server{reports: reports, registry: registry, forward: forward, scan: scan, terms: terms}
	s.register(api)
	return api
}

func (s *Server) register(api huma.API) {
	intake := []string{"trust-intake"}
	huma.Register(api, huma.Operation{OperationID: "submitReport", Method: http.MethodPost, Path: "/api/v1/trust/reports",
		Summary: "Submit a report on a subject (dedup / rate-limit / weight / aggregate)", Tags: intake}, s.submitReport)
	huma.Register(api, huma.Operation{OperationID: "listSubjectKinds", Method: http.MethodGet, Path: "/api/v1/trust/subject-kinds",
		Summary: "List the calling site's registered subject kinds", Tags: intake}, s.listSubjectKinds)
	huma.Register(api, huma.Operation{OperationID: "ensureSubjectKinds", Method: http.MethodPost, Path: "/api/v1/trust/subject-kinds/ensure",
		Summary: "Declaratively ensure the calling site's subject kinds (idempotent; site from the client binding; never revives deprecated)", Tags: intake}, s.ensureSubjectKinds)
	huma.Register(api, huma.Operation{OperationID: "listReportReasons", Method: http.MethodGet, Path: "/api/v1/trust/report-reasons",
		Summary: "List the calling site's usable report reasons (global base + own extensions, non-deprecated)", Tags: intake}, s.listReportReasons)
	huma.Register(api, huma.Operation{OperationID: "submitScan", Method: http.MethodPost, Path: "/api/v1/trust/scan",
		Summary: "Submit a content-scan event for async AI shadow-scoring (accept-type; site derived from the client binding)", Tags: intake}, s.submitScan)

	// Tier0 sync word-list check (step 05). Stateless, deterministic, channel-
	// independent (no AI gateway): the caller asks whether text trips the word
	// list and gets allow/deny/hold back. Site three-state mirrors scan/forward.
	check := []string{"trust-check"}
	huma.Register(api, huma.Operation{OperationID: "checkText", Method: http.MethodPost, Path: "/api/v1/trust/check",
		Summary: "Synchronous Tier0 word-list check (deterministic; allow/deny/hold; no state written)", Tags: check}, s.checkText)

	// community→trust convergence (step 03). These carry `site` in the body
	// (unlike /reports, which derives it from the client binding) because a
	// forwarder relays for many community-backed sites through one S2S identity;
	// the KUN_TRUST_FORWARDER_CLIENT_IDS allowlist is the counterweight.
	fwd := []string{"trust-forward"}
	huma.Register(api, huma.Operation{OperationID: "forwardReviewItem", Method: http.MethodPost, Path: "/api/v1/trust/forward",
		Summary: "Forward a product's local review signal into the unified inbox (allowlist-gated)", Tags: fwd}, s.forwardReviewItem)
	huma.Register(api, huma.Operation{OperationID: "resolveForwardedItem", Method: http.MethodPost, Path: "/api/v1/trust/forward/resolve",
		Summary: "Close a forwarded item after a site's local decision (no disposition, no callback)", Tags: fwd}, s.resolveForwardedItem)
}

type forwardInput struct{ Body dto.ForwardRequest }
type forwardOutput struct {
	Body Envelope[dto.ForwardResponse]
}

func (s *Server) forwardReviewItem(ctx context.Context, in *forwardInput) (*forwardOutput, error) {
	res, err := s.forward.Forward(ctx, service.ForwardParams{
		CallerClientID: callerClientID(ctx),
		Site:           in.Body.Site, SubjectKind: in.Body.SubjectKind, SubjectID: in.Body.SubjectID,
		Severity: in.Body.Severity, WeightSum: in.Body.WeightSum, ContextNote: in.Body.ContextNote,
	})
	if err != nil {
		return nil, mapForwardErr("forward", err)
	}
	return &forwardOutput{Body: okEnvelope(dto.ForwardResponse{
		ReviewItemID: res.ReviewItemID, Created: res.Created,
	})}, nil
}

type resolveInput struct{ Body dto.ForwardResolveRequest }
type resolveOutput struct {
	Body Envelope[dto.ForwardResolveResponse]
}

func (s *Server) resolveForwardedItem(ctx context.Context, in *resolveInput) (*resolveOutput, error) {
	res, err := s.forward.Resolve(ctx, service.ResolveParams{
		CallerClientID: callerClientID(ctx),
		ReviewItemID:   in.Body.ReviewItemID, Outcome: in.Body.Outcome, ActorRef: in.Body.ActorRef,
	})
	if err != nil {
		return nil, mapForwardErr("resolve", err)
	}
	return &resolveOutput{Body: okEnvelope(dto.ForwardResolveResponse{Closed: res.Closed})}, nil
}

// callerClientID returns the authenticated S2S client id (empty when unbound).
func callerClientID(ctx context.Context) string {
	if c := clientFromCtx(ctx); c != nil {
		return c.ID
	}
	return ""
}

// mapForwardErr translates a forward/resolve service error into the house envelope.
func mapForwardErr(op string, err error) *houseError {
	switch {
	case stderrors.Is(err, service.ErrForwarderNotAllowed):
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"client is not an allowed trust forwarder")
	case stderrors.Is(err, service.ErrSubjectKindNotRegistered):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed,
			"subject_kind is not registered for this site")
	case stderrors.Is(err, service.ErrInvalidOutcome):
		return apiErrMsg(http.StatusBadRequest, errors.ErrValidationFailed, "outcome must be approved or rejected")
	case stderrors.Is(err, service.ErrReviewItemNotFound):
		return apiErr(http.StatusNotFound, errors.ErrNotFound)
	default:
		slog.Error("trust "+op, "err", err)
		return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
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
		SubjectURL: in.Body.SubjectURL,
		ReporterID: in.Body.ReporterID,
	})
	if err != nil {
		return nil, mapIntakeErr("submit report", err)
	}
	return &submitReportOutput{Body: okEnvelope(dto.ReportResponse{
		ReportID: res.ReportID, ReviewItemID: res.ReviewItemID,
	})}, nil
}

type submitScanInput struct{ Body dto.ScanRequest }
type submitScanOutput struct {
	Body Envelope[dto.ScanResponse]
}

func (s *Server) submitScan(ctx context.Context, in *submitScanInput) (*submitScanOutput, error) {
	// Site three-state (step 04): an empty wire `site` derives from the client
	// binding (the accept-type default); a non-empty wire `site` is the forwarder
	// relay path, allowlist-gated in the service (mirrors the forward face). Only
	// the default path needs a bound site, so siteBinding is consulted only then.
	boundSite := ""
	if in.Body.Site == "" {
		site, he := siteBinding(ctx)
		if he != nil {
			return nil, he
		}
		boundSite = site
	}
	res, err := s.scan.Ingest(ctx, service.ScanParams{
		CallerClientID: callerClientID(ctx),
		Site:           boundSite, WireSite: in.Body.Site,
		SubjectKind: in.Body.SubjectKind, SubjectID: in.Body.SubjectID,
		Text: in.Body.Text, AuthorID: in.Body.AuthorID,
	})
	if err != nil {
		// mapForwardErr covers both the allowlist 403 and the registry 422 the scan
		// face now shares with the forward face.
		return nil, mapForwardErr("submit scan", err)
	}
	return &submitScanOutput{Body: okEnvelope(dto.ScanResponse{
		ScanID: res.ScanID, Truncated: res.Truncated,
	})}, nil
}

type checkInput struct{ Body dto.CheckRequest }
type checkOutput struct {
	Body Envelope[dto.CheckResponse]
}

func (s *Server) checkText(ctx context.Context, in *checkInput) (*checkOutput, error) {
	// Site three-state (mirrors submitScan): an empty wire `site` derives from the
	// client binding; a non-empty wire `site` is the allowlist-gated relay path
	// (checked in the service). Only the default path needs a bound site.
	boundSite := ""
	if in.Body.Site == "" {
		site, he := siteBinding(ctx)
		if he != nil {
			return nil, he
		}
		boundSite = site
	}
	res, err := s.terms.Check(ctx, service.CheckParams{
		CallerClientID: callerClientID(ctx),
		Site:           boundSite, WireSite: in.Body.Site,
		Text: in.Body.Text, AuthorID: in.Body.AuthorID,
	})
	if err != nil {
		// mapForwardErr covers the allowlist 403; a snapshot DB error is a 500.
		return nil, mapForwardErr("check", err)
	}
	return &checkOutput{Body: okEnvelope(dto.CheckResponse{
		Decision: res.Decision, Matched: res.Matched,
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

type ensureSubjectKindsInput struct{ Body dto.EnsureSubjectKindsRequest }
type ensureSubjectKindsOutput struct {
	Body Envelope[dto.EnsureSubjectKindsResponse]
}

// ensureSubjectKinds converges the calling site's subject-kind registry to the
// declared list. The tenant `site` is STRICTLY the client binding (self-site
// only — a relay-tenant ensure is a triggered follow-up, not v1). Empty list →
// empty result; over the 50-kind cap → 422. Deprecated kinds are never revived
// (that is an admin decision).
func (s *Server) ensureSubjectKinds(ctx context.Context, in *ensureSubjectKindsInput) (*ensureSubjectKindsOutput, error) {
	site, he := siteBinding(ctx)
	if he != nil {
		return nil, he
	}
	if len(in.Body.Kinds) > maxEnsureKinds {
		return nil, apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed,
			"at most 50 kinds per ensure call")
	}
	results, err := s.registry.EnsureSubjectKinds(ctx, nil, site, toEnsureItems(in.Body.Kinds))
	if err != nil {
		return nil, mapIntakeErr("ensure subject kinds", err)
	}
	return &ensureSubjectKindsOutput{Body: okEnvelope(dto.EnsureSubjectKindsResponse{
		Results: toEnsureResultViews(results),
	})}, nil
}

type listReportReasonsOutput struct {
	Body Envelope[dto.ReportReasonsResponse]
}

func (s *Server) listReportReasons(ctx context.Context, _ *struct{}) (*listReportReasonsOutput, error) {
	site, he := siteBinding(ctx)
	if he != nil {
		return nil, he
	}
	reasons, err := s.registry.ListUsableReasons(ctx, site)
	if err != nil {
		return nil, mapIntakeErr("list report reasons", err)
	}
	return &listReportReasonsOutput{Body: okEnvelope(dto.ReportReasonsResponse{
		Reasons: toReasonViews(reasons),
	})}, nil
}

// mapIntakeErr translates an intake service error into the house envelope.
func mapIntakeErr(op string, err error) *houseError {
	switch {
	case stderrors.Is(err, service.ErrSubjectKindNotRegistered):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed,
			"subject_kind is not registered for this site")
	case stderrors.Is(err, service.ErrInvalidSubjectURL):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed,
			"subject_url must be an absolute http(s) link of at most 512 chars")
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
