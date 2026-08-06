package handler

import (
	"context"
	"log/slog"
	"net/http"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/service"
	"api/internal/platform/editing"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// The first ops of the user-token write plane: the best-cover vote, cast by the
// voter's own browser instead of relayed by a product backend.
//
// Same service, same table, same advisory semantics as the S2S pair in
// cover_votes.go — deliberately UNCHANGED. Only the provenance of the two
// identity values differs, which is the entire point of the face: the S2S ops
// take (site, actor) from the request body, these take them from the token.
// A first-party product should move to these; the S2S pair stays for backends
// that genuinely act for a user they authenticated themselves.

// UserServer holds the dependencies of the user-plane operations.
type UserServer struct{ votes *service.CoverVoteService }

// SetupUser builds the user-token Huma API. Auth is applied by the caller as
// path-scoped Fiber middleware (middleware.JWTAuth + UserGate) on the
// /api/v1/user/catalog prefix BEFORE this — Huma registers on the app, so the
// group middleware does not cover these routes, and that prefix is disjoint
// from /api/v1/catalog and /api/v1/admin/catalog so neither of those chains can
// intercept a user call. Callable with nil dependencies for spec export.
//
// The editing engine and its per-family permission resolvers are the SAME
// instances the S2S face was set up with (wave 177) — one engine, two faces, so
// the policy a proposal meets never depends on which door it came through.
func SetupUser(app *fiber.App, votes *service.CoverVoteService, engine *editing.Engine, perms PermResolvers) huma.API {
	InstallErrorEnvelope()

	cfg := huma.DefaultConfig("KUN Catalog User API", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""

	api := humafiber.New(app, cfg)
	api.UseMiddleware(UserBridge)

	RegisterUserOps(api, votes)
	RegisterUserEditOps(api, engine, perms)
	return api
}

// RegisterUserOps registers the user-plane operations on an API. SetupUser
// calls it for the runtime face; cmd/gen-openapi calls it on the catalog S2S
// spec API so the exported docs/catalog/openapi.yaml describes BOTH write
// planes in one contract document — the two prefixes never collide, and a
// consumer choosing between them wants to read them side by side.
func RegisterUserOps(api huma.API, votes *service.CoverVoteService) {
	s := &UserServer{votes: votes}
	tags := []string{"catalog-user"}

	huma.Register(api, huma.Operation{
		OperationID: "voteCatalogWorkCoverUser", Method: http.MethodPut,
		Path:    UserPrefix + "/works/{workID}/covers/{coverID}/vote",
		Summary: "Vote for this work's best cover AS THE BEARER TOKEN'S OWN USER. No body: the voter is the token's user and the site is the token's client's catalog site. ONE ballot per user per WORK — voting a different cover MOVES the vote. Advisory only: votes never reorder covers and never touch the editorial pins",
		Tags:    tags,
	}, s.voteCover)
	huma.Register(api, huma.Operation{
		OperationID: "unvoteCatalogWorkCoverUser", Method: http.MethodDelete,
		Path:    UserPrefix + "/works/{workID}/covers/{coverID}/vote",
		Summary: "Withdraw the bearer token's own best-cover vote on this work. Idempotent (no vote to withdraw is still a 200); the cover id is part of the symmetric path and need not match the voted one — a user holds at most one ballot per work",
		Tags:    tags,
	}, s.unvoteCover)
}

// userCoverVoteInput carries ONLY the path. There is deliberately no body type:
// the two values an S2S caller asserts (site, actor) are exactly the two this
// face refuses to accept from the wire.
type userCoverVoteInput struct {
	WorkID  int64 `path:"workID" minimum:"1" doc:"Catalog work id"`
	CoverID int64 `path:"coverID" minimum:"1" doc:"catalog_work_cover row id, as returned by the work detail's covers[].id"`
}

func (s *UserServer) voteCover(ctx context.Context, in *userCoverVoteInput) (*coverVoteOutput, error) {
	uid, site, he := userActor(ctx)
	if he != nil {
		return nil, he
	}
	count, err := s.votes.Vote(ctx, service.CoverVoteParams{
		WorkID: in.WorkID, CoverID: in.CoverID, ActorUID: uid, Site: site,
	})
	if err != nil {
		return nil, coverVoteErr(err)
	}
	return &coverVoteOutput{Body: okEnvelope(dto.CoverVoteResponse{
		CoverID: in.CoverID, VoteCount: count, Voted: true,
	})}, nil
}

func (s *UserServer) unvoteCover(ctx context.Context, in *userCoverVoteInput) (*coverVoteOutput, error) {
	uid, _, he := userActor(ctx)
	if he != nil {
		return nil, he
	}
	if err := s.votes.Unvote(ctx, in.WorkID, uid); err != nil {
		return nil, coverVoteErr(err)
	}
	count, err := s.votes.CountFor(ctx, in.CoverID)
	if err != nil {
		slog.Error("catalog user cover vote count", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	return &coverVoteOutput{Body: okEnvelope(dto.CoverVoteResponse{
		CoverID: in.CoverID, VoteCount: count, Voted: false,
	})}, nil
}
