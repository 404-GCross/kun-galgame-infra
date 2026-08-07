package handler

import (
	"context"
	"log/slog"
	"net/http"

	"api/internal/platform/catalog/dto"
	catperm "api/internal/platform/catalog/perm"
	"api/internal/platform/catalog/service"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

// The CLAIMS face on the user-token write plane (wave 179).
//
// Waves 177-178 moved the editing engine's human writes here; this moves the
// other half of what a person does to a catalog entry — submitting a work,
// walking its claim through the lifecycle, and reading back "my claims". Same
// service, same state machine, same event ledger as the S2S face in
// lifecycle.go. What moves is only where the two identity values come from:
//
//	S2S  — the product backend named the tenant in `site` and asserted the user
//	       in `actor`, and was believed because it authenticated itself
//	       (retired, wave 185).
//	user — the tenant is the token client's catalog_site and the user is the
//	       token's `id` claim. The body has no field for either.
//
// Wave 185 retired the S2S pair outright, leaving these the only way to submit
// a work or move a claim. The one S2S claims op that survives is the READ
// listCatalogClaimsByUser, which forum renders OTHER users' profile pages from
// and which has no user-face equivalent by design: `mine` is the token's own
// list and nobody else's.
//
// AUTHORITY splits three ways here, one line each:
//
//   - review actions (approve/decline/ban/unban) — the token's roles must
//     resolve catalog.claim.review, exactly as the S2S face resolves the
//     asserted actor's. The DB permission overlay hot-swaps that resolver, so a
//     grant made in the permission console takes effect on the next request.
//   - owner actions (submit/publish/withdraw) — take it if it is free, refuse it
//     if it is somebody else's. A claim already owned by another user is a 403;
//     an UNOWNED claim is allowed and adopts the caller as its owner
//     (catalog_work.owner_user_id, write-once) in the same statement. The second
//     half is the product's main gesture, not a leniency: the registry's bulk is
//     machine-imported mirror stock sitting in `draft` with no owner, and the
//     forum wizard's "claim this game" is a person calling `publish` on one of
//     them — refusing it would have 403'd the whole feature. The first half is
//     the tooth the S2S plane never had: there the asserted uid was taken on
//     faith and only the tenant was checked, so any of a site's users could have
//     been made to move any of that site's claims.
//   - claim (none→draft) — any authenticated user, because the action IS the
//     birth of a claim on an unanchored work; it stamps ownership the same way.
//
// The tenant is passed to the service for every action, review ones included,
// so the existing tenancy check answers a cross-tenant call with a 403 without
// this face repeating it.

// UserClaimServer holds the claims face's single dependency.
type UserClaimServer struct{ claims *service.ClaimLifecycleService }

// RegisterUserClaimOps registers the user-plane claim operations. Called by
// SetupUser for the runtime face and by cmd/gen-openapi on the catalog S2S spec
// API, so one contract document describes both write planes. Safe with a nil
// service: spec export never invokes a handler.
func RegisterUserClaimOps(api huma.API, lifecycle *service.ClaimLifecycleService) {
	s := &UserClaimServer{claims: lifecycle}
	tags := []string{"catalog-user"}

	huma.Register(api, huma.Operation{
		OperationID: "submitCatalogWorkUser", Method: http.MethodPost,
		Path:    UserPrefix + "/works/submit",
		Summary: "Submit a work for review AS THE BEARER TOKEN'S OWN USER: mints it in the pending claim state (registry row + content + birth event, one transaction) and stamps the submitter as its owner. The submitting tenant is the token client's catalog site and the submitter is the token's user; the body names neither. product_work_id is OPTIONAL — omit it and the registry issues the identity, the claim adopting the minted work id (returned as product_work_id). IDEMPOTENCY is the S2S op's: with product_work_id a repeat is a 409 echoing the existing work (matched_by=claim); without it a repeat is recognized only by the identity anchors the payload's links assert (matched_by=anchor), and a submission carrying neither WILL mint a second work if retried",
		Tags:    tags,
	}, s.submitWork)
	huma.Register(api, huma.Operation{
		OperationID: "actOnCatalogClaimUser", Method: http.MethodPost,
		Path:    UserPrefix + "/works/{id}/claim-actions/{action}",
		Summary: "Move a claim through its lifecycle AS THE BEARER TOKEN'S OWN USER: claim / submit / publish / withdraw (the token's user must be the entry's owner — or its FIRST CLAIMANT when the entry is unowned, in which case the action adopts it: this is how a person claims one of the machine-imported drafts) or approve / decline / ban / unban (the token's roles must carry catalog.claim.review). 409 on an illegal transition, echoing the current state; 403 on ANOTHER user's claim or another tenant's",
		Tags:    tags,
	}, s.act)
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogClaimsMine", Method: http.MethodGet,
		Path:    UserPrefix + "/claims/mine",
		Summary: "The claims the BEARER TOKEN'S OWN USER has acted on, on the token client's catalog site: current state, latest transition and reason, most recent activity first (cursor: before=last_event_id). The total is the per-user statistic — 'published by me' is this call with claim_state=live&limit=1",
		Tags:    tags,
	}, s.mine)
}

// ---- submission mint ----

type userSubmitWorkInput struct {
	Body dto.UserWorkSubmitRequest
}

func (s *UserClaimServer) submitWork(ctx context.Context, in *userSubmitWorkInput) (*submitWorkOutput, error) {
	uid, site, he := userActor(ctx)
	if he != nil {
		return nil, he
	}
	params := service.SubmitWorkParams{Site: site, ActorUID: uid, Fields: in.Body.Fields}
	if id := in.Body.ProductWorkID; id != nil {
		params.ProductWorkID = *id
	}
	if d := in.Body.Released; d != nil {
		params.Released = service.ReleaseDate{Y: d.Y, M: d.M, D: d.D}
	}
	res, err := s.claims.SubmitWork(ctx, params)
	if err != nil {
		return nil, submitErr(err)
	}
	return &submitWorkOutput{Body: okEnvelope(dto.WorkSubmitResponse{
		WorkID: res.WorkID, ProductWorkID: res.ProductWorkID, ClaimState: res.ClaimState,
		EventID: res.EventID, ReleaseID: res.ReleaseID,
	})}, nil
}

// ---- lifecycle actions ----

type userClaimActionInput struct {
	ID     int64  `path:"id" minimum:"1"`
	Action string `path:"action" doc:"claim | submit | publish | withdraw | approve | decline | ban | unban"`
	Body   dto.UserClaimActionRequest
}

func (s *UserClaimServer) act(ctx context.Context, in *userClaimActionInput) (*claimActionOutput, error) {
	action := service.ClaimAction(in.Action)
	if _, known := service.TransitionRule(action); !known {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, "unknown claim action "+in.Action)
	}
	uid, site, he := userActor(ctx)
	if he != nil {
		return nil, he
	}
	review := service.ReviewActions[action]
	if review {
		// The same cap the open face's queue view applies (wave 186b): judging
		// other people's submissions is a property of the PAIR (person x
		// first-party client), so a token issued through a THIRD-PARTY developer
		// application is never a moderation surface, whatever roles the person
		// behind it holds. Refused BEFORE the permission is even consulted, so
		// the message never doubles as a probe for who is staff.
		if isThirdPartyClient(clientFromCtx(ctx)) {
			return nil, apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
				"a third-party application is not a moderation surface; claim review needs a first-party site client")
		}
		if !catperm.Resolver.Can(userRolesFromCtx(ctx), catperm.ClaimReview) {
			return nil, apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
				"reviewing a claim requires the "+string(catperm.ClaimReview)+" permission")
		}
	}
	res, err := s.claims.Act(ctx, service.ClaimActionParams{
		WorkID: in.ID, Action: action,
		// The tenant is passed even for a review action — unlike the S2S face,
		// which blanks it so a curator can decide across tenants. A moderator
		// reaching this face does so through ONE product's client, and the site
		// that client is bound to is the only one it may moderate; the platform-
		// wide queue is the staff face's job (/api/v1/admin/catalog), which has a
		// staff JWT rather than a per-product token behind it.
		Site:          site,
		ProductWorkID: in.Body.ProductWorkID,
		ActorUID:      uid,
		Reason:        in.Body.Reason,
		// Personal ownership is settled for the owner actions only; the service
		// ignores the flag for `claim` (which stamps ownership by itself) and for
		// the review actions, whose authority was just resolved above.
		RequireOwner: !review,
	})
	if err != nil {
		return nil, claimErr(err)
	}
	return &claimActionOutput{Body: okEnvelope(*res)}, nil
}

// ---- "my claims" ----

type userMineClaimsInput struct {
	ClaimState string `query:"claim_state" doc:"Comma-separated subset of none, live, draft, pending, declined, hidden; absent = every state"`
	Before     int64  `query:"before" doc:"Exclusive cursor: return works whose last_event_id is smaller (0 = first page)"`
	Limit      int    `query:"limit" doc:"Page size (default 20, max 100)"`
}

func (s *UserClaimServer) mine(ctx context.Context, in *userMineClaimsInput) (*userClaimsOutput, error) {
	uid, site, he := userActor(ctx)
	if he != nil {
		return nil, he
	}
	// The same parser every other claim_state parameter on this service uses, so
	// the vocabulary cannot drift face to face.
	claimStates, ok := claimStatesPub(in.ClaimState)
	if !ok {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, msgBadClaimState)
	}
	items, total, err := s.claims.ClaimsByActor(ctx, service.UserClaimQuery{
		ActorUID: uid, Site: site, ClaimStates: claimStates,
		Before: in.Before, Limit: in.Limit,
	})
	if err != nil {
		slog.Error("catalog claims mine", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	// Pre-sized non-nil so an empty page serializes `[]`, not `null`.
	page := dto.CursorPage[service.UserClaimItem]{
		Items: make([]service.UserClaimItem, 0, len(items)), Total: total,
	}
	page.Items = append(page.Items, items...)
	if n := len(items); n > 0 {
		page.NextBefore = items[n-1].LastEventID
	}
	return &userClaimsOutput{Body: okEnvelope(page)}, nil
}
