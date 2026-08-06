package handler

import (
	"context"
	stderrors "errors"
	"log/slog"
	"net/http"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/service"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

// The best-cover vote face (wave 175): two S2S ops with which a product backend
// records — on behalf of one of its users — which of a work's covers is the best
// one, and takes that back.
//
// It registers on the SAME Huma API as the rest of the S2S surface, so the
// path-scoped Basic client auth on /api/v1/catalog gates it, and the actor is
// ASSERTED in the body exactly as the lifecycle and editing faces assert theirs.
// The site binding is enforced for both ops: casting a ballot is a write, and a
// client may only write as the tenant it is bound to.
//
// What the votes are NOT: an ordering. The service writes only
// catalog_cover_vote — never sort_order, never portrait_pinned — so an editorial
// pin outranks any number of votes, and the read face hands the counts to
// consumers as decoration they may use or ignore. NSFW is likewise not this
// face's business: the per-image sexual/violence columns already trim the read
// side, and a cover nobody may see collects votes nobody will render.

// CoverVoteServer holds the vote face's dependencies.
type CoverVoteServer struct{ votes *service.CoverVoteService }

// SetupCoverVotes registers the two vote ops on the S2S Huma API built by
// Setup. Callable with a nil service for spec export.
func SetupCoverVotes(api huma.API, votes *service.CoverVoteService) {
	s := &CoverVoteServer{votes: votes}
	tags := []string{"catalog-s2s"}

	huma.Register(api, huma.Operation{
		OperationID: "voteCatalogWorkCover", Method: http.MethodPut,
		Path:    "/api/v1/catalog/works/{workID}/covers/{coverID}/vote",
		Summary: "Record the asserted user's vote for this work's best cover. ONE ballot per user per WORK: voting a different cover MOVES the vote rather than adding one. Returns the voted cover's new total. Advisory only — votes never reorder covers and never touch the editorial pins",
		Tags:    tags,
	}, s.vote)
	huma.Register(api, huma.Operation{
		OperationID: "unvoteCatalogWorkCover", Method: http.MethodDelete,
		Path:    "/api/v1/catalog/works/{workID}/covers/{coverID}/vote",
		Summary: "Withdraw the asserted user's best-cover vote on this work. Idempotent (no vote to withdraw is still a 200); the cover id is part of the symmetric path and is not required to match the voted one — a user holds at most one ballot per work",
		Tags:    tags,
	}, s.unvote)
}

type coverVoteInput struct {
	WorkID  int64 `path:"workID" minimum:"1" doc:"Catalog work id"`
	CoverID int64 `path:"coverID" minimum:"1" doc:"catalog_work_cover row id, as returned by the work detail's covers[].id"`
	Body    dto.CoverVoteRequest
}

type coverVoteOutput struct {
	Body Envelope[dto.CoverVoteResponse]
}

func (s *CoverVoteServer) vote(ctx context.Context, in *coverVoteInput) (*coverVoteOutput, error) {
	if he := enforceSiteBinding(clientFromCtx(ctx), in.Body.Site); he != nil {
		return nil, he
	}
	count, err := s.votes.Vote(ctx, service.CoverVoteParams{
		WorkID: in.WorkID, CoverID: in.CoverID,
		ActorUID: in.Body.Actor.UserID, Site: in.Body.Site,
	})
	if err != nil {
		return nil, coverVoteErr(err)
	}
	return &coverVoteOutput{Body: okEnvelope(dto.CoverVoteResponse{
		CoverID: in.CoverID, VoteCount: count, Voted: true,
	})}, nil
}

// unvote takes the ballot back. The response carries the voted cover's count so
// a caller can re-render without a second read — and since the withdrawal is
// idempotent, that count is simply the current one.
func (s *CoverVoteServer) unvote(ctx context.Context, in *coverVoteInput) (*coverVoteOutput, error) {
	if he := enforceSiteBinding(clientFromCtx(ctx), in.Body.Site); he != nil {
		return nil, he
	}
	if err := s.votes.Unvote(ctx, in.WorkID, in.Body.Actor.UserID); err != nil {
		return nil, coverVoteErr(err)
	}
	count, err := s.votes.CountFor(ctx, in.CoverID)
	if err != nil {
		slog.Error("catalog cover vote count", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	return &coverVoteOutput{Body: okEnvelope(dto.CoverVoteResponse{
		CoverID: in.CoverID, VoteCount: count, Voted: false,
	})}, nil
}

// coverVoteErr maps the vote refusals onto the house envelope. A missing actor
// is the caller's malformed request (400); an unavailable work or a cover that
// is not this work's are both "that thing is not there to vote on" (404).
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
