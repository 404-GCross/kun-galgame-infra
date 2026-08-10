package handler

import (
	"context"
	"log/slog"
	"net/http"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/service"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

func (s *LifecycleServer) registerUserClaims(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogClaimsByUser", Method: http.MethodGet,
		Path:    "/api/v1/catalog/users/{uid}/claims",
		Summary: "The claims a user has acted on: current state, latest transition and reason, most recent activity first (cursor: before=last_event_id)",
		Tags:    []string{"catalog-lifecycle"},
	}, s.userClaims)
}

type userClaimsInput struct {
	UID        int64  `path:"uid" minimum:"1" doc:"The product-side user id recorded as the event actor"`
	Site       string `query:"site" doc:"Restrict to one claiming site"`
	ClaimState string `query:"claim_state" doc:"Comma-separated subset of none, live, draft, pending, declined, hidden; absent = every state"`
	Before     int64  `query:"before" doc:"Exclusive cursor: return works whose last_event_id is smaller (0 = first page)"`
	Limit      int    `query:"limit" doc:"Page size (default 20, max 100)"`
}

type userClaimsOutput struct {
	Body Envelope[dto.CursorPage[service.UserClaimItem]]
}

func (s *LifecycleServer) userClaims(ctx context.Context, in *userClaimsInput) (*userClaimsOutput, error) {
	claimStates, ok := claimStatesPub(in.ClaimState)
	if !ok {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, msgBadClaimState)
	}
	items, total, err := s.claims.ClaimsByActor(ctx, service.UserClaimQuery{
		ActorUID: in.UID, Site: in.Site, ClaimStates: claimStates,
		Before: in.Before, Limit: in.Limit,
	})
	if err != nil {
		slog.Error("catalog claims by user", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	page := dto.CursorPage[service.UserClaimItem]{
		Items: make([]service.UserClaimItem, 0, len(items)), Total: total,
	}
	page.Items = append(page.Items, items...)
	if n := len(items); n > 0 {
		page.NextBefore = items[n-1].LastEventID
	}
	return &userClaimsOutput{Body: okEnvelope(page)}, nil
}
