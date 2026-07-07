package handler

import (
	"context"
	"encoding/base64"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// S2SServer holds the dependencies of the S2S operations.
type S2SServer struct {
	resolve *service.ResolveService
	work    *service.WorkService
}

// Setup builds the S2S Huma API (resolve / redirect feed / work claim) over
// the Fiber app. Auth (S2SAuth) is applied by the caller as path-scoped Fiber
// middleware BEFORE this — Huma registers on the app, so the caller must
// gate the /api/v1/catalog prefix. Callable with nil services for spec
// export (handlers are never invoked then).
func Setup(app *fiber.App, resolve *service.ResolveService, work *service.WorkService) huma.API {
	InstallErrorEnvelope()

	cfg := huma.DefaultConfig("KUN Catalog Service", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""

	api := humafiber.New(app, cfg)
	api.UseMiddleware(S2SBridge)

	s := &S2SServer{resolve: resolve, work: work}
	s.register(api)
	return api
}

func (s *S2SServer) register(api huma.API) {
	tags := []string{"catalog-s2s"}
	huma.Register(api, huma.Operation{
		OperationID: "resolveCatalogIDs", Method: http.MethodPost, Path: "/api/v1/catalog/resolve",
		Summary: "Resolve entity ids to their canonical ids (redirect following, batch)", Tags: tags,
	}, s.resolveBatch)
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogRedirects", Method: http.MethodGet, Path: "/api/v1/catalog/redirects",
		Summary: "Keyset feed of redirects for product-site id cleanup crons", Tags: tags,
	}, s.redirects)
	huma.Register(api, huma.Operation{
		OperationID: "claimCatalogWork", Method: http.MethodPost, Path: "/api/v1/catalog/works/claim",
		Summary: "Claim (or register) the catalog work for a product-side work", Tags: tags,
	}, s.claim)
}

// ---- resolve ----

type resolveInput struct {
	Body dto.ResolveRequest
}

type resolveOutput struct {
	Body Envelope[dto.ResolveResponse]
}

func (s *S2SServer) resolveBatch(ctx context.Context, in *resolveInput) (*resolveOutput, error) {
	mappings, err := s.resolve.ResolveBatch(ctx, in.Body.EntityType, in.Body.IDs)
	if err != nil {
		slog.Error("catalog resolve", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	out := dto.ResolveResponse{Mappings: make(map[string]int64, len(mappings))}
	for old, canonical := range mappings {
		out.Mappings[strconv.FormatInt(old, 10)] = canonical
		if old != canonical {
			out.Redirected = append(out.Redirected, old)
		}
	}
	return &resolveOutput{Body: okEnvelope(out)}, nil
}

// ---- redirect feed ----

type redirectsInput struct {
	// -1 = no filter (Huma cannot express optional scalars as pointers).
	EntityType int16  `query:"entity_type" default:"-1" doc:"Filter to one entity type; -1 = all"`
	Cursor     string `query:"cursor" doc:"Opaque cursor from the previous page's next_cursor"`
	Limit      int    `query:"limit" doc:"Page size (max 1000)"`
}

type redirectsOutput struct {
	Body Envelope[dto.RedirectFeedResponse]
}

func (s *S2SServer) redirects(ctx context.Context, in *redirectsInput) (*redirectsOutput, error) {
	cursor, err := decodeRedirectCursor(in.Cursor)
	if err != nil {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, "malformed cursor")
	}
	limit := in.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	// The feed orders by merged_at (always set by the merge path — manual
	// redirect inserts are not a thing; see repository.RedirectCursor).
	items, next, err := s.resolve.RedirectsSince(ctx, optionalFilter(in.EntityType), cursor, limit)
	if err != nil {
		slog.Error("catalog redirects feed", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	out := dto.RedirectFeedResponse{Items: make([]dto.RedirectItem, len(items))}
	for i, item := range items {
		out.Items[i] = dto.RedirectItem{
			EntityType: item.EntityType, OldID: item.OldID, CurrentID: item.CurrentID,
			MergedAt: item.MergedAt,
		}
	}
	if len(items) == limit {
		out.NextCursor = encodeRedirectCursor(next)
	}
	return &redirectsOutput{Body: okEnvelope(out)}, nil
}

// Cursor wire format: base64("<merged_at unixnano>:<entity_type>:<old_id>").
// Opaque to clients; only ever produced and parsed here.

func encodeRedirectCursor(c repository.RedirectCursor) string {
	raw := fmt.Sprintf("%d:%d:%d", c.MergedAt.UnixNano(), c.EntityType, c.OldID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeRedirectCursor(s string) (repository.RedirectCursor, error) {
	if s == "" {
		return repository.RedirectCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return repository.RedirectCursor{}, err
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 3 {
		return repository.RedirectCursor{}, stderrors.New("bad cursor arity")
	}
	nano, err1 := strconv.ParseInt(parts[0], 10, 64)
	et, err2 := strconv.ParseInt(parts[1], 10, 16)
	old, err3 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return repository.RedirectCursor{}, stderrors.New("bad cursor fields")
	}
	return repository.RedirectCursor{MergedAt: time.Unix(0, nano), EntityType: int16(et), OldID: old}, nil
}

// ---- work claim ----

type claimInput struct {
	Body dto.ClaimWorkRequest
}

type claimOutput struct {
	Body Envelope[dto.ClaimWorkResponse]
}

func (s *S2SServer) claim(ctx context.Context, in *claimInput) (*claimOutput, error) {
	client := clientFromCtx(ctx)
	if he := enforceSiteBinding(client, in.Body.Site); he != nil {
		return nil, he
	}
	anchors := make([]service.ExternalAnchor, len(in.Body.Anchors))
	matchedBy := "import:claim:" + client.ID
	for i, a := range in.Body.Anchors {
		anchors[i] = service.ExternalAnchor{SourceID: a.SourceID, ExternalID: a.ExternalID, MatchedBy: matchedBy}
	}
	workID, created, err := s.work.ClaimWork(ctx, service.ClaimWorkParams{
		MediumID:      in.Body.MediumID,
		Site:          in.Body.Site,
		ProductWorkID: in.Body.ProductWorkID,
		DisplayName:   in.Body.DisplayName,
		OLang:         in.Body.OLang,
		ContentRating: in.Body.ContentRating,
		Anchors:       anchors,
	})
	if err != nil {
		if stderrors.Is(err, service.ErrClaimConflict) {
			return nil, apiErrMsg(http.StatusConflict, errors.ErrOperationFailed, err.Error())
		}
		slog.Error("catalog claim", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	return &claimOutput{Body: okEnvelope(dto.ClaimWorkResponse{WorkID: workID, Created: created})}, nil
}
