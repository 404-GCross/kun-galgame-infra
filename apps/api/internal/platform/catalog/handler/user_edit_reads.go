package handler

import (
	"context"
	"net/http"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/editing"
	"api/pkg/errors"
)

type userEditSnapshotInput struct {
	EntityType string `query:"entity_type" minLength:"1" doc:"Registered entity type, e.g. catalog.work"`
	EntityID   int64  `query:"entity_id" minimum:"1"`
}

func (s *UserEditServer) snapshot(ctx context.Context, in *userEditSnapshotInput) (*editSnapshotOutput, error) {
	if _, _, he := userActor(ctx); he != nil {
		return nil, he
	}
	values, err := s.engine.CurrentSnapshot(ctx, in.EntityType, in.EntityID)
	if err != nil {
		return nil, editErr(err)
	}
	return &editSnapshotOutput{Body: okEnvelope(dto.EditSnapshotResponse{
		EntityType: in.EntityType, EntityID: in.EntityID, Values: values,
	})}, nil
}

type userEditListInput struct {
	EntityType string `query:"entity_type" minLength:"1" doc:"Entity type to list (required: authority is resolved per type)"`
	EntityID   int64  `query:"entity_id" doc:"Narrow to one entity; 0 = the whole type"`
	Status     string `query:"status" enum:",open,merged,declined,withdrawn" doc:"Filter by status; empty = all"`
	Limit      int    `query:"limit" doc:"Page size (max 200, default 50)"`
	Mine       bool   `query:"mine" doc:"true = only the token user's own proposals (no review permission needed); false/absent = the review queue for this entity type, which requires review authority"`
}

func (s *UserEditServer) list(ctx context.Context, in *userEditListInput) (*editListOutput, error) {
	actor, site, he := userEditActor(ctx)
	if he != nil {
		return nil, he
	}
	status := int16(-1)
	if in.Status != "" {
		found := false
		for code, name := range editing.StatusName {
			if name == in.Status {
				status, found = code, true
				break
			}
		}
		if !found {
			return nil, apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed, "unknown status")
		}
	}

	filter := editing.ProposalFilter{
		EntityType: in.EntityType, EntityID: in.EntityID,
		Site:   site,
		Status: status, Limit: in.Limit,
	}
	if in.Mine {
		filter.ProposerUID = actor.UserID
	} else {
		ok, err := s.mayReview(ctx, actor, site, in.EntityType, in.EntityID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
				"reading the review queue for "+in.EntityType+" requires review authority on it; pass mine=true for one's own proposals")
		}
	}

	items, total, err := s.engine.ListProposalsWithTotal(ctx, filter)
	if err != nil {
		return nil, editErr(err)
	}
	resp := dto.EditProposalListResponse{Items: make([]dto.EditProposalView, 0, len(items)), Total: total}
	for i := range items {
		resp.Items = append(resp.Items, proposalView(&items[i]))
	}
	return &editListOutput{Body: okEnvelope(resp)}, nil
}

func (s *UserEditServer) mayReview(ctx context.Context, actor dto.EditActor, site, entityType string, entityID int64) (bool, error) {
	fields, err := s.engine.SchemaProjection(ctx, entityType, entityID,
		s.policyCtx(ctx, actor, site, familyOf(entityType)))
	if err != nil {
		return false, editErr(err)
	}
	for _, f := range fields {
		if f.CanReview {
			return true, nil
		}
	}
	return false, nil
}
