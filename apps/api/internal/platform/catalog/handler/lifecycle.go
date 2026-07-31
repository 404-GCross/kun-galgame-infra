package handler

import (
	"context"
	stderrors "errors"
	"log/slog"
	"net/http"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/editspec"
	catperm "api/internal/platform/catalog/perm"
	"api/internal/platform/catalog/service"
	"api/internal/platform/editing"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// The claim-lifecycle S2S face (wave 155 W2/W3): the eight semantic actions and
// the two cursor feeds downstream products build their inboxes from.
//
// It registers on the SAME Huma API as the rest of the S2S surface, so the
// path-scoped Basic client auth on /api/v1/catalog already gates it, and the
// actor is ASSERTED in the body exactly as the editing face asserts it — one
// convention for "a product backend is acting on behalf of one of its users",
// not two.
//
// Authority split follows 03 定案 §3: the four OWNER actions require the
// client's catalog_site binding to match the claim's site, the four REVIEW
// actions require the asserted actor to hold catalog.claim.review (wave 157 —
// a NEW key granted moderator and up, not the ren-only catalog.review, because
// judging submissions is moderation and the surface it replaces was staffed by
// moderators). No new global ROLE is minted for either.
//
// The two feeds are ascending-by-id with an exclusive `since`, the shape both
// wiki feeds they replace use, because that is what the downstream crons
// (forum's Redis cursor, moyu's SQL cursor) already know how to consume. The
// field NAMES are catalog-native: this is a new id space and reusing the wiki
// DTO's spelling would invite a consumer to assume the old semantics.

// LifecycleServer holds the lifecycle face's dependencies.
type LifecycleServer struct {
	claims *service.ClaimLifecycleService
	engine *editing.Engine
	perms  PermResolvers
}

// SetupLifecycle registers the lifecycle actions and the two feeds on the S2S
// Huma API built by Setup. Callable with nil dependencies for spec export.
func SetupLifecycle(api huma.API, claims *service.ClaimLifecycleService, engine *editing.Engine, perms PermResolvers) {
	s := &LifecycleServer{claims: claims, engine: engine, perms: perms}
	tags := []string{"catalog-lifecycle"}

	huma.Register(api, huma.Operation{
		OperationID: "actOnCatalogClaim", Method: http.MethodPost,
		Path:    "/api/v1/catalog/works/{id}/claim-actions/{action}",
		Summary: "Move a claim through its lifecycle: claim / submit / publish / withdraw (owner) or approve / decline / ban / unban (review). 409 on an illegal transition, echoing the current state",
		Tags:    tags,
	}, s.act)
	huma.Register(api, huma.Operation{
		OperationID: "submitCatalogWork", Method: http.MethodPost,
		Path:    "/api/v1/catalog/works/submit",
		Summary: "Mint a work in the pending claim state from a submission form (one transaction: registry row + content + birth event). Repeat submits for the same site + product_work_id are a 409 echoing the existing work",
		Tags:    tags,
	}, s.submitWork)
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogClaimEvents", Method: http.MethodGet,
		Path:    "/api/v1/catalog/claim-events/feed",
		Summary: "Cursor feed of claim-state transitions (ascending id; the source for downstream inboxes and point awards)",
		Tags:    tags,
	}, s.claimEvents)
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogEditRevisions", Method: http.MethodGet,
		Path:    "/api/v1/catalog/edit-revisions/feed",
		Summary: "Cursor feed of editing-engine revisions across all entities (ascending id; filterable by entity family/type)",
		Tags:    tags,
	}, s.editRevisions)
	s.registerUserClaims(api)
}

// ---- actions ----

type claimActionInput struct {
	ID     int64  `path:"id" minimum:"1"`
	Action string `path:"action" doc:"claim | submit | publish | withdraw | approve | decline | ban | unban"`
	Body   dto.ClaimActionRequest
}

type claimActionOutput struct {
	Body Envelope[service.ClaimActionResult]
}

func (s *LifecycleServer) act(ctx context.Context, in *claimActionInput) (*claimActionOutput, error) {
	action := service.ClaimAction(in.Action)
	if _, known := service.TransitionRule(action); !known {
		return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, "unknown claim action "+in.Action)
	}
	client := clientFromCtx(ctx)
	if service.ReviewActions[action] {
		// Review authority is the asserted user's, resolved through the catalog
		// family's own vocabulary — the same fail-closed shape the editing face
		// uses for its review rules.
		if !catperm.Resolver.Can(in.Body.Actor.Roles, catperm.ClaimReview) {
			return nil, apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
				"reviewing a claim requires the "+string(catperm.ClaimReview)+" permission")
		}
	} else if he := enforceSiteBinding(client, in.Body.Site); he != nil {
		return nil, he
	}
	site := in.Body.Site
	if service.ReviewActions[action] {
		// A curator acts across tenants; the event still records the claim's own
		// site, which the service reads off the work.
		site = ""
	}
	res, err := s.claims.Act(ctx, service.ClaimActionParams{
		WorkID: in.ID, Action: action, Site: site,
		ProductWorkID: optionalID(in.Body.ProductWorkID),
		ActorUID:      in.Body.Actor.UserID, Reason: in.Body.Reason,
	})
	if err != nil {
		return nil, claimErr(err)
	}
	return &claimActionOutput{Body: okEnvelope(*res)}, nil
}

// ---- submission mint ----

type submitWorkInput struct {
	Body dto.WorkSubmitRequest
}

type submitWorkOutput struct {
	Body Envelope[dto.WorkSubmitResponse]
}

// submitWork is the OWNER half of the lifecycle face: no review permission is
// involved, only the client's site binding, because filing a submission is what
// a product's own users do. The asserted actor becomes the birth event's actor,
// which is what makes "my submissions" answerable later (wave 157's per-user
// face reads exactly these rows).
func (s *LifecycleServer) submitWork(ctx context.Context, in *submitWorkInput) (*submitWorkOutput, error) {
	if he := enforceSiteBinding(clientFromCtx(ctx), in.Body.Site); he != nil {
		return nil, he
	}
	params := service.SubmitWorkParams{
		Site: in.Body.Site, ProductWorkID: in.Body.ProductWorkID,
		ActorUID: in.Body.Actor.UserID, Fields: in.Body.Fields,
	}
	if d := in.Body.Released; d != nil {
		params.Released = service.ReleaseDate{Y: d.Y, M: d.M, D: d.D}
	}
	res, err := s.claims.SubmitWork(ctx, params)
	if err != nil {
		return nil, submitErr(err)
	}
	return &submitWorkOutput{Body: okEnvelope(dto.WorkSubmitResponse{
		WorkID: res.WorkID, ClaimState: res.ClaimState,
		EventID: res.EventID, ReleaseID: res.ReleaseID,
	})}, nil
}

// submitErr maps the mint's refusals. The two 409s are different facts and stay
// distinguishable: "you already submitted this" carries the existing work, and
// the mirror gate carries the facet that is still owned by a retiring duty-chain
// step (both are "the world, not your request" — hence 409, not 422).
func submitErr(err error) error {
	var (
		exists     *service.ClaimExistsError
		fieldErr   *editspec.SubmissionFieldError
		mirrorGate *editspec.MirrorGateError
	)
	switch {
	case stderrors.As(err, &exists):
		return apiErrData(http.StatusConflict, errors.ErrOperationFailed, exists.Error(),
			dto.WorkSubmitConflictInfo{WorkID: exists.WorkID, CurrentState: exists.CurrentState})
	case stderrors.As(err, &mirrorGate):
		return apiErrMsg(http.StatusConflict, errors.ErrOperationFailed, mirrorGate.Error())
	case stderrors.As(err, &fieldErr),
		stderrors.Is(err, service.ErrSubmitTargetRequired),
		stderrors.Is(err, service.ErrSubmitDisplayNameRequired),
		stderrors.Is(err, service.ErrSubmitInvalidDate):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed, err.Error())
	}
	slog.Error("catalog work submit", "err", err)
	return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
}

// optionalID turns the wire's 0-means-absent into a pointer (Huma cannot
// express an optional scalar as one).
func optionalID(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}

// claimErr maps lifecycle errors onto the house envelope. The transition error
// is a 409 carrying the current state, so a caller that raced another actor can
// re-render without a second read.
func claimErr(err error) error {
	var (
		transition *service.ClaimTransitionError
		ownership  *service.ClaimOwnershipError
	)
	switch {
	case stderrors.Is(err, gorm.ErrRecordNotFound):
		return apiErrMsg(http.StatusNotFound, errors.ErrNotFound, "work not found")
	case stderrors.As(err, &transition):
		return apiErrData(http.StatusConflict, errors.ErrOperationFailed, transition.Error(),
			dto.ClaimTransitionInfo{CurrentState: transition.Current, AllowedFrom: transition.Allowed})
	case stderrors.As(err, &ownership):
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden, ownership.Error())
	case stderrors.Is(err, service.ErrClaimReasonRequired),
		stderrors.Is(err, service.ErrClaimTargetRequired):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed, err.Error())
	}
	slog.Error("catalog claim action", "err", err)
	return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
}

// ---- claim-event feed ----

type claimEventsInput struct {
	Since int64  `query:"since" doc:"Exclusive cursor: return events with a greater id (0 = from the beginning)"`
	Limit int    `query:"limit" doc:"Page size (default 200, max 1000)"`
	Site  string `query:"site" doc:"Restrict to one claiming site"`
	// Wave 157: the same feed, narrowed to one user's own transitions — a
	// product rendering one person's activity should not have to filter the
	// global stream client-side.
	ActorUID int64 `query:"actor_uid" doc:"Restrict to transitions caused by this user (0 = every actor)"`
}

type claimEventsOutput struct {
	Body Envelope[dto.ClaimEventFeed]
}

func (s *LifecycleServer) claimEvents(ctx context.Context, in *claimEventsInput) (*claimEventsOutput, error) {
	items, err := s.claims.EventsSince(ctx, in.Since, in.Limit, in.Site, in.ActorUID)
	if err != nil {
		slog.Error("catalog claim event feed", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	out := dto.ClaimEventFeed{Items: make([]dto.ClaimEventFeedItem, len(items))}
	for i, e := range items {
		out.Items[i] = dto.ClaimEventFeedItem{
			ID: e.ID, WorkID: e.WorkID, FromState: e.FromState, ToState: e.ToState,
			ActorUID: e.ActorUID, Reason: e.Reason, Site: e.Site,
			ProductWorkID: e.ProductWorkID, CreatedAt: e.CreatedAt,
		}
	}
	if n := len(items); n > 0 {
		out.NextSince = items[n-1].ID
	} else {
		out.NextSince = in.Since
	}
	return &claimEventsOutput{Body: okEnvelope(out)}, nil
}

// ---- edit-revision feed ----

type revisionFeedInput struct {
	Since        int64  `query:"since" doc:"Exclusive cursor: return revisions with a greater id (0 = from the beginning)"`
	Limit        int    `query:"limit" doc:"Page size (default 200, max 1000)"`
	EntityFamily string `query:"entity_family" doc:"Restrict to one family, e.g. catalog"`
	EntityType   string `query:"entity_type" doc:"Restrict to one type, e.g. catalog.work"`
}

type revisionFeedOutput struct {
	Body Envelope[dto.EditRevisionFeed]
}

func (s *LifecycleServer) editRevisions(ctx context.Context, in *revisionFeedInput) (*revisionFeedOutput, error) {
	revs, err := s.engine.RevisionsSince(ctx, editing.RevisionFeedFilter{
		Since: in.Since, Limit: in.Limit,
		EntityFamily: in.EntityFamily, EntityType: in.EntityType,
	})
	if err != nil {
		slog.Error("catalog edit revision feed", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	out := dto.EditRevisionFeed{Items: make([]dto.EditRevisionFeedItem, len(revs))}
	for i, r := range revs {
		out.Items[i] = dto.EditRevisionFeedItem{
			ID: r.ID, EntityFamily: r.EntityFamily, EntityType: r.EntityType,
			EntityID: r.EntityID, Seq: r.Seq, Action: r.Action,
			ChangedFields: decodeStrings(r.ChangedFields),
			ActorUID:      r.ActorUID, AmenderUID: r.AmenderUID,
			ProposalID: r.ProposalID, Site: r.Site, CreatedAt: r.CreatedAt,
		}
	}
	if n := len(revs); n > 0 {
		out.NextSince = revs[n-1].ID
	} else {
		out.NextSince = in.Since
	}
	return &revisionFeedOutput{Body: okEnvelope(out)}, nil
}
