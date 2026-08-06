package handler

import (
	"context"
	"net/http"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/editing"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
)

// The editing engine on the user-token write plane (wave 177).
//
// Wave 176 opened this face with the cover ballot — a write whose entire
// payload is a path. These three ops are the real subject: filing an edit,
// taking it back, and asking what the caller is allowed to change. They run the
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
}

// userEditActor derives the policy actor from the verified token, and is the
// only way these ops learn who is writing.
//
// TrustTier is always 0 and IsEntityOwner is always false, BY DESIGN rather
// than by omission. Neither has an infra-side source: trust tiers live in the
// product's own trust ledger, and "owns this entity" is a product-side fact
// about who created the row. Both therefore stay on the S2S face, where the
// backend that holds those facts asserts them — letmoe's ProposeTrusted lane
// and kungal's owner-review lane keep running there. Manufacturing either value
// here (e.g. inferring ownership from the revision log) would put a policy
// input in the hands of the face that exists precisely to have no such inputs.
// When the claim lifecycle migrates onto user tokens, ownership becomes a fact
// the catalog itself holds, and this comment is the place to revisit.
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
	actor, site, he := userEditActor(ctx)
	if he != nil {
		return nil, he
	}
	prop, _, _, err := s.engine.GetProposal(ctx, in.ID)
	if err != nil {
		return nil, editErr(err)
	}
	// The tenancy line the S2S face draws with the client's catalog_site
	// binding is drawn here with the token client's — a proposal filed on
	// another tenant is not this caller's to close, and the engine's own check
	// is about the proposer only. Same 403 either way.
	if prop.Site != site {
		return nil, apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"the proposal belongs to another catalog tenant")
	}
	if err := s.engine.WithdrawProposal(ctx, in.ID, s.policyCtx(actor, prop.Site, prop.EntityFamily)); err != nil {
		return nil, editErr(err)
	}
	return s.closedView(ctx, in.ID)
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
