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

type LifecycleServer struct {
	claims *service.ClaimLifecycleService
	engine *editing.Engine
	perms  PermResolvers
}

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

type claimActionOutput struct {
	Body Envelope[service.ClaimActionResult]
}

type submitWorkOutput struct {
	Body Envelope[dto.WorkSubmitResponse]
}

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
	case stderrors.As(err, &notOwned):
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden, notOwned.Error())
	case stderrors.Is(err, service.ErrClaimReasonRequired),
		stderrors.Is(err, service.ErrClaimTargetRequired):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed, err.Error())
	}
	slog.Error("catalog claim action", "err", err)
	return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
}

type claimEventsInput struct {
	Since    int64  `query:"since" doc:"Exclusive cursor: return events with a greater id (0 = from the beginning)"`
	Limit    int    `query:"limit" doc:"Page size (default 200, max 1000)"`
	Site     string `query:"site" doc:"Restrict to one claiming site"`
	ActorUID int64  `query:"actor_uid" doc:"Restrict to transitions caused by this user (0 = every actor)"`
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
