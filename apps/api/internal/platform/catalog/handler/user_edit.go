package handler

import (
	"context"
	"fmt"
	"net/http"

	"api/internal/platform/catalog/dto"
	catperm "api/internal/platform/catalog/perm"
	"api/internal/platform/editing"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

const maxThirdPartyOpenProposals = 20

type UserEditServer struct{ *EditServer }

func RegisterUserEditOps(api huma.API, engine *editing.Engine, perms PermResolvers) {
	s := &UserEditServer{EditServer: &EditServer{engine: engine, perms: perms}}
	tags := []string{"catalog-user"}

	huma.Register(api, huma.Operation{
		OperationID: "createEditProposalUser", Method: http.MethodPost,
		Path:    UserPrefix + "/edit/proposals",
		Summary: "File an edit proposal AS THE BEARER TOKEN'S OWN USER (automerges into a direct edit when the caller's token roles already carry the review capability, and NEVER when the token was issued through a third-party application). The proposer and the filing tenant are derived from the token; the body carries neither",
		Tags:    tags,
	}, s.create)
	huma.Register(api, huma.Operation{
		OperationID: "withdrawEditProposalUser", Method: http.MethodPost,
		Path:    UserPrefix + "/edit/proposals/{id}/withdraw",
		Summary: "Withdraw one's OWN open proposal. Bodiless: the only identity involved is the token's, and the engine refuses any proposal the token's user did not file",
		Tags:    tags,
	}, s.withdraw)
	huma.Register(api, huma.Operation{
		OperationID: "getEditSchemaUser", Method: http.MethodGet,
		Path:    UserPrefix + "/edit/schema/{entity_type}",
		Summary: "Field schema + THIS TOKEN's evaluated field-level capabilities. Same projection as the S2S op, with no actor query parameters at all: a caller cannot ask what some other user would be allowed to do",
		Tags:    tags,
	}, s.schema)

	huma.Register(api, huma.Operation{
		OperationID: "getEditProposalUser", Method: http.MethodGet,
		Path:    UserPrefix + "/edit/proposals/{id}",
		Summary: "Read one proposal with its amendments and effective patch (same shape as the S2S detail read); refuses a proposal filed on another tenant",
		Tags:    tags,
	}, s.get)
	huma.Register(api, huma.Operation{
		OperationID: "amendEditProposalUser", Method: http.MethodPost,
		Path:    UserPrefix + "/edit/proposals/{id}/amendments",
		Summary: "Amend an open proposal AS THE BEARER TOKEN'S OWN USER (set/unset fields; requires the review rule on every touched field). The amender is the token; the body names nobody",
		Tags:    tags,
	}, s.amend)
	huma.Register(api, huma.Operation{
		OperationID: "mergeEditProposalUser", Method: http.MethodPost,
		Path:    UserPrefix + "/edit/proposals/{id}/merge",
		Summary: "Merge an open proposal AS THE BEARER TOKEN'S OWN USER (per-field rebase; 409 lists conflicts). Review authority comes from the token's roles or from catalog-held entity ownership",
		Tags:    tags,
	}, s.merge)
	huma.Register(api, huma.Operation{
		OperationID: "declineEditProposalUser", Method: http.MethodPost,
		Path:    UserPrefix + "/edit/proposals/{id}/decline",
		Summary: "Decline an open proposal with a reason AS THE BEARER TOKEN'S OWN USER",
		Tags:    tags,
	}, s.decline)
	huma.Register(api, huma.Operation{
		OperationID: "revertEditEntityUser", Method: http.MethodPost,
		Path:    UserPrefix + "/edit/revert",
		Summary: "Restore an entity to a historical revision AS THE BEARER TOKEN'S OWN USER (a new revision; history kept). The tenant whose overlay applies is the token client's, so the body carries no site",
		Tags:    tags,
	}, s.revert)

	huma.Register(api, huma.Operation{
		OperationID: "getEditSnapshotUser", Method: http.MethodGet,
		Path:    UserPrefix + "/edit/snapshot",
		Summary: "The entity's current registered-field values (the editor's bootstrap read), same shape as the S2S op. Authenticated but NOT tenant-fenced: it projects the same entity state the public reads already render",
		Tags:    tags,
	}, s.snapshot)
	huma.Register(api, huma.Operation{
		OperationID: "listEditProposalsUser", Method: http.MethodGet,
		Path:    UserPrefix + "/edit/proposals",
		Summary: "List edit proposals on the token client's catalog site. mine=true is the token user's OWN filing history (no permission needed); mine absent is the REVIEW QUEUE and requires the same review authority the merge/decline ops need for that entity_type (403 otherwise). Neither site nor proposer_uid is a parameter",
		Tags:    tags,
	}, s.list)
}

func userEditActor(ctx context.Context) (dto.EditActor, string, *houseError) {
	uid, site, he := userActor(ctx)
	if he != nil {
		return dto.EditActor{}, "", he
	}
	roles := userRolesFromCtx(ctx)
	actor := dto.EditActor{UserID: uid, Roles: roles}
	if catperm.Resolver.Can(roles, catperm.EditTrusted) && !isThirdPartyClient(clientFromCtx(ctx)) {
		actor.TrustTier = editing.TrustedTier
	}
	return actor, site, nil
}

type userEditCreateInput struct {
	Body dto.UserEditProposalCreateRequest
}

func (s *UserEditServer) create(ctx context.Context, in *userEditCreateInput) (*editCreateOutput, error) {
	actor, site, he := userEditActor(ctx)
	if he != nil {
		return nil, he
	}
	if err := s.refuseIfThirdPartyCap(ctx, actor.UserID); err != nil {
		return nil, err
	}
	prop, rev, err := s.engine.CreateProposal(ctx, editing.CreateProposalInput{
		EntityType: in.Body.EntityType, EntityID: in.Body.EntityID,
		Patch: in.Body.Patch, Note: in.Body.Note,
		Actor: s.policyCtx(ctx, actor, site, familyOf(in.Body.EntityType)),
	})
	if err != nil {
		return nil, editErr(err)
	}
	resp := dto.EditProposalCreateResponse{Proposal: proposalView(prop), Merged: rev != nil}
	if rev != nil {
		rv := revisionView(rev)
		resp.Revision = &rv
	}
	return &editCreateOutput{Body: okEnvelope(resp)}, nil
}

type userEditWithdrawInput struct {
	ID int64 `path:"id" minimum:"1" doc:"Proposal id (must be one the token's user filed)"`
}

func (s *UserEditServer) refuseIfThirdPartyCap(ctx context.Context, uid int64) error {
	if !isThirdPartyClient(clientFromCtx(ctx)) {
		return nil
	}
	_, total, err := s.engine.ListProposalsWithTotal(ctx, editing.ProposalFilter{
		ProposerUID: uid,
		Status:      editing.StatusOpen,
		Limit:       1,
	})
	if err != nil {
		return editErr(err)
	}
	if total >= maxThirdPartyOpenProposals {
		return apiErrMsg(http.StatusTooManyRequests, errors.ErrTooManyRequests,
			fmt.Sprintf("you already have %d proposals awaiting review; wait for a decision or withdraw one before filing another",
				maxThirdPartyOpenProposals))
	}
	return nil
}

func (s *UserEditServer) withdraw(ctx context.Context, in *userEditWithdrawInput) (*editCloseOutput, error) {
	actor, prop, err := s.proposalForUser(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := s.engine.WithdrawProposal(ctx, in.ID, s.policyCtx(ctx, actor, prop.Site, prop.EntityFamily)); err != nil {
		return nil, editErr(err)
	}
	return s.closedView(ctx, in.ID)
}

func (s *UserEditServer) proposalForUser(ctx context.Context, id int64) (dto.EditActor, *editing.Proposal, error) {
	actor, site, he := userEditActor(ctx)
	if he != nil {
		return dto.EditActor{}, nil, he
	}
	prop, _, _, err := s.engine.GetProposal(ctx, id)
	if err != nil {
		return dto.EditActor{}, nil, editErr(err)
	}
	if prop.Site != site {
		return dto.EditActor{}, nil, apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"the proposal belongs to another catalog tenant")
	}
	return actor, prop, nil
}

type userEditSchemaInput struct {
	EntityType string `path:"entity_type" doc:"Registered entity type, e.g. catalog.work"`
	EntityID   int64  `query:"entity_id" doc:"Entity-aware projection subject (0 = type-level projection)"`
}

func (s *UserEditServer) schema(ctx context.Context, in *userEditSchemaInput) (*editSchemaOutput, error) {
	actor, site, he := userEditActor(ctx)
	if he != nil {
		return nil, he
	}
	fields, err := s.engine.SchemaProjection(ctx, in.EntityType, in.EntityID,
		s.policyCtx(ctx, actor, site, familyOf(in.EntityType)))
	if err != nil {
		return nil, editErr(err)
	}
	resp := dto.EditSchemaResponse{
		EntityType: in.EntityType,
		Fields:     make([]dto.EditSchemaFieldView, 0, len(fields)),
	}
	for _, f := range fields {
		resp.Fields = append(resp.Fields, dto.EditSchemaFieldView{
			Key: f.Key, Kind: string(f.Kind), DiffHint: f.DiffHint, Deprecated: f.Deprecated,
			Locked: f.Locked, CanPropose: f.CanPropose, CanReview: f.CanReview,
			WouldAutomerge: f.WouldAutomerge,
			MaxSuppressed:  f.MaxSuppressed, MaxElements: f.MaxElements,
		})
	}
	return &editSchemaOutput{Body: okEnvelope(resp)}, nil
}

func (s *UserEditServer) get(ctx context.Context, in *editGetInput) (*editGetOutput, error) {
	_, site, he := userEditActor(ctx)
	if he != nil {
		return nil, he
	}
	prop, amendments, eff, err := s.engine.GetProposal(ctx, in.ID)
	if err != nil {
		return nil, editErr(err)
	}
	if prop.Site != site {
		return nil, apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"the proposal belongs to another catalog tenant")
	}
	view := proposalView(prop)
	view.EffectivePatch = eff
	view.Amendments = amendmentViews(amendments)
	return &editGetOutput{Body: okEnvelope(view)}, nil
}

type userEditAmendInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body dto.UserEditAmendRequest
}

func (s *UserEditServer) amend(ctx context.Context, in *userEditAmendInput) (*editAmendOutput, error) {
	actor, prop, err := s.proposalForUser(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	amendment, aerr := s.engine.AmendProposal(ctx, in.ID, editing.AmendInput{
		Set: in.Body.Set, Unset: in.Body.Unset, Note: in.Body.Note,
		Actor: s.policyCtx(ctx, actor, prop.Site, prop.EntityFamily),
	})
	if aerr != nil {
		return nil, editErr(aerr)
	}
	views := amendmentViews([]editing.ProposalAmendment{*amendment})
	return &editAmendOutput{Body: okEnvelope(views[0])}, nil
}

type userEditDecisionInput struct {
	ID   int64 `path:"id" minimum:"1"`
	Body dto.UserEditDecisionRequest
}

func (s *UserEditServer) merge(ctx context.Context, in *userEditDecisionInput) (*editMergeOutput, error) {
	actor, prop, err := s.proposalForUser(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	rev, merr := s.engine.MergeProposal(ctx, in.ID, s.policyCtx(ctx, actor, prop.Site, prop.EntityFamily), in.Body.Note)
	if merr != nil {
		return nil, editErr(merr)
	}
	return &editMergeOutput{Body: okEnvelope(revisionView(rev))}, nil
}

func (s *UserEditServer) decline(ctx context.Context, in *userEditDecisionInput) (*editCloseOutput, error) {
	actor, prop, err := s.proposalForUser(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if derr := s.engine.DeclineProposal(ctx, in.ID, s.policyCtx(ctx, actor, prop.Site, prop.EntityFamily), in.Body.Note); derr != nil {
		return nil, editErr(derr)
	}
	return s.closedView(ctx, in.ID)
}

type userEditRevertInput struct {
	Body dto.UserEditRevertRequest
}

func (s *UserEditServer) revert(ctx context.Context, in *userEditRevertInput) (*editRevertOutput, error) {
	actor, site, he := userEditActor(ctx)
	if he != nil {
		return nil, he
	}
	prop, rev, err := s.engine.Revert(ctx, editing.RevertInput{
		EntityType: in.Body.EntityType, EntityID: in.Body.EntityID, ToSeq: in.Body.ToSeq,
		Note: in.Body.Note, Actor: s.policyCtx(ctx, actor, site, familyOf(in.Body.EntityType)),
	})
	if err != nil {
		return nil, editErr(err)
	}
	return &editRevertOutput{Body: okEnvelope(dto.EditRevertResponse{
		Proposal: proposalView(prop), Revision: revisionView(rev),
	})}, nil
}
