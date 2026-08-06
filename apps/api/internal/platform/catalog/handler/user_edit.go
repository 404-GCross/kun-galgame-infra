package handler

import (
	"context"
	"net/http"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/editing"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

// The editing engine on the user-token write plane (waves 177-178).
//
// Wave 176 opened this face with the cover ballot — a write whose entire
// payload is a path. Wave 177 brought the real subject: filing an edit, taking
// it back, and asking what the caller is allowed to change. Wave 178 completes
// it with the MODERATION ops — amend / merge / decline / revert — and the
// proposal detail read, so a first-party product needs no S2S mirror of its own
// permission gates: the same token that files an edit reviews one. They run the
// SAME engine, the same registry, the same per-family permission vocabulary and
// the same site overlays as the S2S ops in edit.go. Nothing about the editing
// model moves here. What moves is where the actor comes from:
//
//	S2S  — the product backend asserts {user_id, roles, trust_tier,
//	       is_entity_owner} in the body and names the tenant in `site`.
//	user — the uid is the token's `id` claim, the roles are the token's
//	       role union, and the tenant is the token client's catalog_site.
//	       The request body has no field for any of them.
//
// That is why the create body below is EditProposalCreateRequest minus `actor`
// and minus `site`: not a trimmed convenience shape, but the removal of every
// place a caller could name someone else. The S2S pair stays registered for
// backends that genuinely authenticate a user themselves; first-party products
// should move here (docs/catalog/01 §4.2).

// UserEditServer holds the user plane's editing dependencies. It reuses
// EditServer wholesale — policyCtx (family-routed perm resolution) and the
// error mapping are the S2S face's, deliberately, so the two faces cannot drift
// into two different opinions about what a permission means.
type UserEditServer struct{ *EditServer }

// RegisterUserEditOps registers the user-plane editing operations. Called both
// by SetupUser (the runtime face, over its own path-scoped auth chain) and by
// cmd/gen-openapi on the catalog S2S spec API, so the exported contract
// document describes both write planes side by side. Safe with a nil engine and
// nil resolvers: spec export never invokes a handler.
func RegisterUserEditOps(api huma.API, engine *editing.Engine, perms PermResolvers) {
	s := &UserEditServer{EditServer: &EditServer{engine: engine, perms: perms}}
	tags := []string{"catalog-user"}

	huma.Register(api, huma.Operation{
		OperationID: "createEditProposalUser", Method: http.MethodPost,
		Path:    UserPrefix + "/edit/proposals",
		Summary: "File an edit proposal AS THE BEARER TOKEN'S OWN USER (automerges into a direct edit when the caller's token roles already carry the review capability). The proposer and the filing tenant are derived from the token; the body carries neither",
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

	// The moderation ops (wave 178). Same engine calls as their S2S siblings;
	// the only difference is that the reviewer is the token, and that the
	// proposal's tenant is fenced against the token client's catalog_site
	// instead of against the S2S client's binding.
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
}

// userEditActor derives the policy actor from the verified token, and is the
// only way these ops learn who is writing.
//
// TrustTier is always 0 and IsEntityOwner is always false HERE — but ownership
// is no longer lost by that. Wave 178 made it a fact the CATALOG holds
// (catalog_work.owner_user_id, stamped write-once at submission / claim birth)
// and the engine DERIVES it from the spec's OwnerUserID hook, comparing the
// stored uid to the policy context's own uid. So the flag this function leaves
// false is set — by the engine, from data, one layer below any wire — exactly
// when the caller really is the entry's creator, and kungal's owner-review lane
// works here without a backend to assert anything. This is the revisit the
// wave-177 comment asked for.
//
// The S2S face keeps its asserted `is_entity_owner`: derivation can only turn
// the flag ON, never off, so a product backend that knows an ownership the
// catalog does not (a family registering no hook, a product-side notion of
// ownership) is still believed. TrustTier stays 0 because it genuinely has no
// infra-side source — trust tiers live in the product's own ledger, and
// letmoe's ProposeTrusted lane therefore remains an S2S lane.
func userEditActor(ctx context.Context) (dto.EditActor, string, *houseError) {
	uid, site, he := userActor(ctx)
	if he != nil {
		return dto.EditActor{}, "", he
	}
	return dto.EditActor{UserID: uid, Roles: userRolesFromCtx(ctx)}, site, nil
}

type userEditCreateInput struct {
	Body dto.UserEditProposalCreateRequest
}

func (s *UserEditServer) create(ctx context.Context, in *userEditCreateInput) (*editCreateOutput, error) {
	actor, site, he := userEditActor(ctx)
	if he != nil {
		return nil, he
	}
	prop, rev, err := s.engine.CreateProposal(ctx, editing.CreateProposalInput{
		EntityType: in.Body.EntityType, EntityID: in.Body.EntityID,
		Patch: in.Body.Patch, Note: in.Body.Note,
		Actor: s.policyCtx(actor, site, familyOf(in.Body.EntityType)),
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

// userEditWithdrawInput carries only the proposal id. The S2S withdraw request
// is an actor and nothing else, so subtracting the actor leaves no body at all —
// the honest shape, rather than an invented note field the engine would drop
// (WithdrawProposal writes no decision note).
type userEditWithdrawInput struct {
	ID int64 `path:"id" minimum:"1" doc:"Proposal id (must be one the token's user filed)"`
}

func (s *UserEditServer) withdraw(ctx context.Context, in *userEditWithdrawInput) (*editCloseOutput, error) {
	actor, prop, err := s.proposalForUser(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := s.engine.WithdrawProposal(ctx, in.ID, s.policyCtx(actor, prop.Site, prop.EntityFamily)); err != nil {
		return nil, editErr(err)
	}
	return s.closedView(ctx, in.ID)
}

// proposalForUser is this face's counterpart of EditServer.proposalForWrite: it
// derives the actor from the token, loads the proposal, and draws the tenancy
// line the S2S face draws with the S2S client's catalog_site binding using the
// TOKEN client's instead. A proposal filed on another tenant is not this
// caller's to read or decide — 403, before any engine rule is consulted, so a
// cross-tenant caller learns nothing beyond "not yours" (the engine's own checks
// are about the proposer / the field policies, never about the tenant).
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

// userEditSchemaInput is the S2S schema input with every actor-shaped query
// parameter removed: user_id, roles, trust_tier, is_entity_owner and site are
// all derived. entity_id stays, because it names the SUBJECT of the projection
// (an entity-aware overlay evaluates against it), not the caller.
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
		s.policyCtx(actor, site, familyOf(in.EntityType)))
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
		})
	}
	return &editSchemaOutput{Body: okEnvelope(resp)}, nil
}

// ---- the moderation ops (wave 178) -----------------------------------------

// get is the proposal DETAIL read: the same view the S2S op returns (proposal +
// amendments + effective patch), reusing its mappers verbatim. It is fenced by
// tenant like every other op here — the withdraw precedent — because a proposal
// carries its patch, its proposer and its decision note, none of which belong to
// a neighbouring tenant's caller.
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
		Actor: s.policyCtx(actor, prop.Site, prop.EntityFamily),
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
	rev, merr := s.engine.MergeProposal(ctx, in.ID, s.policyCtx(actor, prop.Site, prop.EntityFamily), in.Body.Note)
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
	if derr := s.engine.DeclineProposal(ctx, in.ID, s.policyCtx(actor, prop.Site, prop.EntityFamily), in.Body.Note); derr != nil {
		return nil, editErr(derr)
	}
	return s.closedView(ctx, in.ID)
}

type userEditRevertInput struct {
	Body dto.UserEditRevertRequest
}

// revert restores an entity to a historical revision. There is no proposal to
// fence against, so the tenant is simply the token client's binding — passed as
// the actor's site, which is both the policy-overlay key and the tenant the
// produced proposal/revision rows are attributed to. The engine gates it on the
// review rule per changed field, so the authority is the same one merge needs:
// the token's roles, or the ownership the engine derives from the catalog.
func (s *UserEditServer) revert(ctx context.Context, in *userEditRevertInput) (*editRevertOutput, error) {
	actor, site, he := userEditActor(ctx)
	if he != nil {
		return nil, he
	}
	prop, rev, err := s.engine.Revert(ctx, editing.RevertInput{
		EntityType: in.Body.EntityType, EntityID: in.Body.EntityID, ToSeq: in.Body.ToSeq,
		Note: in.Body.Note, Actor: s.policyCtx(actor, site, familyOf(in.Body.EntityType)),
	})
	if err != nil {
		return nil, editErr(err)
	}
	return &editRevertOutput{Body: okEnvelope(dto.EditRevertResponse{
		Proposal: proposalView(prop), Revision: revisionView(rev),
	})}, nil
}
