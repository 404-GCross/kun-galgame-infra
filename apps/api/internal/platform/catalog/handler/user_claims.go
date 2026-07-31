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

// The per-user claim face (wave 157 P3): "the claims this user has worked on",
// which downstream products render as 我的投稿 / 我的草稿 and as the counters on
// a profile page.
//
// It is a READ face on the S2S surface, gated by the same path-scoped Basic
// client auth as everything else under /api/v1/catalog — the uid is a path
// parameter rather than an asserted actor because a product backend asks it on
// behalf of a page it is rendering, exactly as it asks the edit face for
// ?proposer_uid=. Nothing here is writable and nothing here is private to the
// registry: it is the product's own users' activity, coming back to it.
//
// No dedicated stats endpoint accompanies it (the wave's explicit instruction):
// the `total` on this page IS the stat. "Works published by me" = this call with
// claim_state=live&limit=1; "submissions awaiting review" = claim_state=pending;
// "submitted today" is countable off the first page, since the list is ordered
// by most recent activity and carries first_acted_at per row.

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
	// Parsed by the SAME helper every other claim_state parameter on this
	// service uses, so the vocabulary cannot drift face to face.
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
	// Pre-sized non-nil so an empty page serializes `[]`, not `null`.
	page := dto.CursorPage[service.UserClaimItem]{
		Items: make([]service.UserClaimItem, 0, len(items)), Total: total,
	}
	page.Items = append(page.Items, items...)
	if n := len(items); n > 0 {
		// The cursor for the page below this one. It stays 0 on an empty page,
		// which is the end-of-list signal: a consumer that pages until
		// next_before is 0 can neither loop nor restart from the top.
		page.NextBefore = items[n-1].LastEventID
	}
	return &userClaimsOutput{Body: okEnvelope(page)}, nil
}
