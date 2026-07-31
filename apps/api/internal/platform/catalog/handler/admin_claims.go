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

// The staff side of the claim lifecycle (wave 155 W2): the review queue and a
// proxy for the four curator actions.
//
// It is a PROXY, not a second implementation: both faces call the same
// ClaimLifecycleService.Act, so the state machine, the event row and the touch
// have exactly one definition. What differs is only where the authority comes
// from — here the operator's JWT plus the catalog.claim.review permission
// AdminGate enforces on this subtree, so no role is asserted in a body and no
// site binding applies (a moderator works across tenants). That is the SAME key
// the S2S face checks on its asserted actor (wave 157): one authority, two ways
// of learning who the actor is.
//
// 03 定案 §3 is explicit that the queue needs no dedicated message table: it is
// "claim_state = pending, oldest first", which is exactly this query.

func (s *AdminServer) registerClaims(api huma.API) {
	tags := []string{"catalog-admin"}
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogPendingClaims", Method: http.MethodGet,
		Path:    "/api/v1/admin/catalog/claims/pending",
		Summary: "The claim review queue: submissions awaiting a decision, oldest submission first",
		Tags:    tags,
	}, s.listPendingClaims)
	huma.Register(api, huma.Operation{
		OperationID: "actOnCatalogClaimAsStaff", Method: http.MethodPost,
		Path:    "/api/v1/admin/catalog/claims/{id}/{action}",
		Summary: "Approve / decline / ban / unban a claim (decline and ban record the reason on the event)",
		Tags:    tags,
	}, s.actOnClaim)
}

type pendingClaimsInput struct {
	Site  string `query:"site" doc:"Restrict to one claiming site"`
	Limit int    `query:"limit" doc:"Items per page (max 200)"`
}

type pendingClaimsOutput struct {
	Body Envelope[dto.Page[service.PendingClaimItem]]
}

func (s *AdminServer) listPendingClaims(ctx context.Context, in *pendingClaimsInput) (*pendingClaimsOutput, error) {
	items, total, err := s.claims.PendingClaims(ctx, in.Site, in.Limit)
	if err != nil {
		slog.Error("catalog admin pending claims", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	return &pendingClaimsOutput{Body: okEnvelope(dto.Page[service.PendingClaimItem]{Items: items, Total: total})}, nil
}

type staffClaimActionInput struct {
	ID     int64  `path:"id" minimum:"1"`
	Action string `path:"action" doc:"approve | decline | ban | unban"`
	Body   dto.StaffClaimActionRequest
}

type staffClaimActionOutput struct {
	Body Envelope[service.ClaimActionResult]
}

func (s *AdminServer) actOnClaim(ctx context.Context, in *staffClaimActionInput) (*staffClaimActionOutput, error) {
	action := service.ClaimAction(in.Action)
	// The staff face carries the FOUR review actions only. The owner actions
	// (claim/submit/publish/withdraw) belong to the product that owns the claim
	// and are reachable on the S2S face; exposing them here would let a curator
	// publish on a site's behalf, which is a different authority than reviewing.
	if !service.ReviewActions[action] {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam,
			"unknown review action "+in.Action+" (approve, decline, ban, unban)")
	}
	res, err := s.claims.Act(ctx, service.ClaimActionParams{
		WorkID: in.ID, Action: action,
		ActorUID: adminIDFromCtx(ctx), Reason: in.Body.Reason,
	})
	if err != nil {
		return nil, claimErr(err)
	}
	return &staffClaimActionOutput{Body: okEnvelope(*res)}, nil
}
