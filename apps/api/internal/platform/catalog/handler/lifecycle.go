package handler

import (
	"context"
	stderrors "errors"
	"log/slog"
	"net/http"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/service"
	"api/internal/platform/editing"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// The claim-lifecycle S2S face (wave 155 W2/W3), reduced by wave 185 to what a
// MACHINE reads: the two cursor feeds downstream products build their inboxes
// from, plus the per-user claim list a product renders somebody else's profile
// from (registered in user_claims.go).
//
// The two WRITES that used to live here — the eight semantic actions and the
// submission mint — are gone. Both asserted the acting user as a number in the
// request body, and both have a twin on the user-token plane
// (user_claims_face.go) where the uid is the token's `id` claim and the tenant
// the token client's catalog_site: identity that cannot be typed by the caller.
// A cross-repo sweep and 48h of production logs found no caller left on either,
// so the asserted-actor door is simply shut rather than deprecated in place.
//
// What remains needs no actor at all. The feeds are ascending-by-id with an
// exclusive `since`, the shape both wiki feeds they replaced use, because that
// is what the downstream crons (forum's Redis cursor, moyu's SQL cursor)
// already know how to consume. The field NAMES are catalog-native: this is a
// new id space and reusing the wiki DTO's spelling would invite a consumer to
// assume the old semantics.

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
		OperationID: "listCatalogClaimEvents", Method: http.MethodGet,
		Path:    "/api/v1/catalog/claim-events/feed",
		Summary: "Cursor feed of claim-state transitions (ascending id; the source for downstream inboxes and point awards)",
		Tags:    tags,
	}, s.claimEvents)
	huma.Register(api, huma.Operation{
		OperationID: "listCatalogEditRevisions", Method: http.MethodGet,
		Path:    "/api/v1/catalog/edit-revisions/feed",
		Summary: "Cursor feed of editing-engine revisions across all entities (ascending id; filterable by entity family/type and by site). catalog.work items carry product_work_id, the claiming product's own id for the entity",
		Tags:    tags,
	}, s.editRevisions)
	s.registerUserClaims(api)
}

// ---- shared wire shapes and error mappers ----

// claimActionOutput and submitWorkOutput are the claim writes' envelopes. The
// ops that used to return them here retired in wave 185; they are declared on
// this file still because the user plane (user_claims_face.go) answers with the
// same two shapes, and one declaration is what keeps the two faces from
// drifting into two spellings of one result.
type claimActionOutput struct {
	Body Envelope[service.ClaimActionResult]
}

type submitWorkOutput struct {
	Body Envelope[dto.WorkSubmitResponse]
}

// submitErr maps the mint's refusals. The 409 carries the existing work ("you
// already submitted this" is a fact about the world, not a malformed request).
// It used to have a sibling 409 from the mirror gate; wave 161 retired the duty-
// chain steps that gate protected, so a submission can no longer be refused for
// naming a facet somebody else still writes.
func submitErr(err error) error {
	var (
		exists   *service.ClaimExistsError
		fieldErr *editspec.SubmissionFieldError
	)
	switch {
	case stderrors.As(err, &exists):
		return apiErrData(http.StatusConflict, errors.ErrOperationFailed, exists.Error(),
			dto.WorkSubmitConflictInfo{
				WorkID: exists.WorkID, ProductWorkID: exists.ProductWorkID,
				CurrentState: exists.CurrentState,
				MatchedBy:    exists.MatchedBy, Anchor: exists.Anchor,
			})
	case stderrors.As(err, &fieldErr),
		stderrors.Is(err, service.ErrSubmitTargetRequired),
		stderrors.Is(err, service.ErrSubmitDisplayNameRequired),
		stderrors.Is(err, service.ErrSubmitInvalidDate):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed, err.Error())
	}
	slog.Error("catalog work submit", "err", err)
	return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
}

// claimErr maps lifecycle errors onto the house envelope. The transition error
// is a 409 carrying the current state, so a caller that raced another actor can
// re-render without a second read.
func claimErr(err error) error {
	var (
		transition *service.ClaimTransitionError
		ownership  *service.ClaimOwnershipError
		notOwned   *service.ClaimNotOwnedError
	)
	switch {
	case stderrors.Is(err, gorm.ErrRecordNotFound):
		return apiErrMsg(http.StatusNotFound, errors.ErrNotFound, "work not found")
	case stderrors.As(err, &transition):
		return apiErrData(http.StatusConflict, errors.ErrOperationFailed, transition.Error(),
			dto.ClaimTransitionInfo{CurrentState: transition.Current, AllowedFrom: transition.Allowed})
	case stderrors.As(err, &ownership):
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden, ownership.Error())
	// The user face's personal-ownership refusal (wave 179): the same 403 the
	// tenant refusal answers with, because to the caller both mean "not yours".
	case stderrors.As(err, &notOwned):
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden, notOwned.Error())
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
	Site         string `query:"site" doc:"Restrict to one tenant's revisions, e.g. kungal"`
}

type revisionFeedOutput struct {
	Body Envelope[dto.EditRevisionFeed]
}

func (s *LifecycleServer) editRevisions(ctx context.Context, in *revisionFeedInput) (*revisionFeedOutput, error) {
	revs, err := s.engine.RevisionsSince(ctx, editing.RevisionFeedFilter{
		Since: in.Since, Limit: in.Limit,
		EntityFamily: in.EntityFamily, EntityType: in.EntityType, Site: in.Site,
	})
	if err != nil {
		slog.Error("catalog edit revision feed", "err", err)
		return nil, apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
	}
	claims, err := s.workClaims(ctx, revs)
	if err != nil {
		slog.Error("catalog edit revision feed claims", "err", err)
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
			ProductWorkID: productWorkID(claims, &revs[i]),
		}
	}
	if n := len(revs); n > 0 {
		out.NextSince = revs[n-1].ID
	} else {
		out.NextSince = in.Since
	}
	return &revisionFeedOutput{Body: okEnvelope(out)}, nil
}

// workClaims loads the product-side identity of every catalog.work this page
// mentions — one query for the page, not one per item.
func (s *LifecycleServer) workClaims(ctx context.Context, revs []editing.Revision) (map[int64]service.ClaimIdentity, error) {
	if s.claims == nil {
		return nil, nil
	}
	seen := map[int64]struct{}{}
	ids := make([]int64, 0, len(revs))
	for _, r := range revs {
		if r.EntityType != editspec.TypeWork {
			continue
		}
		if _, dup := seen[r.EntityID]; dup {
			continue
		}
		seen[r.EntityID] = struct{}{}
		ids = append(ids, r.EntityID)
	}
	return s.claims.ClaimIdentities(ctx, ids)
}

// productWorkID resolves one revision's product-side id: only for works, and
// only when the claim's tenant is the revision's own.
func productWorkID(claims map[int64]service.ClaimIdentity, r *editing.Revision) *int64 {
	if r.EntityType != editspec.TypeWork {
		return nil
	}
	c, ok := claims[r.EntityID]
	if !ok || c.Site != r.Site {
		return nil
	}
	return &c.ProductWorkID
}
