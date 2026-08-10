package handler

import (
	"context"
	stderrors "errors"
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

type UserServer struct{ votes *service.CoverVoteService }

func SetupUser(app *fiber.App, votes *service.CoverVoteService, engine *editing.Engine, perms PermResolvers, claims *service.ClaimLifecycleService, read *service.ReadService) huma.API {
	InstallErrorEnvelope()

	cfg := huma.DefaultConfig("KUN Catalog User API", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""

	api := humafiber.New(app, cfg)
	api.UseMiddleware(UserBridge)

	RegisterUserOps(api, votes)
	RegisterUserEditOps(api, engine, perms)
	RegisterUserClaimOps(api, claims)
	RegisterUserReadOps(api, read)
	return api
}

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

type userCoverVoteInput struct {
	WorkID  int64 `path:"workID" minimum:"1" doc:"Catalog work id"`
	CoverID int64 `path:"coverID" minimum:"1" doc:"catalog_work_cover row id, as returned by the work detail's covers[].id"`
}

type coverVoteOutput struct {
	Body Envelope[dto.CoverVoteResponse]
}

func coverVoteErr(err error) error {
	switch {
	case stderrors.Is(err, service.ErrVoteActorRequired):
		return apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, err.Error())
	case stderrors.Is(err, service.ErrVoteWorkUnavailable),
		stderrors.Is(err, service.ErrVoteCoverNotOnWork):
		return apiErrMsg(http.StatusNotFound, errors.ErrNotFound, err.Error())
	}
	slog.Error("catalog cover vote", "err", err)
	return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
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
