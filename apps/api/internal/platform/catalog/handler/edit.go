package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"net/http"
	"strings"

	"api/internal/platform/authz"
	"api/internal/platform/catalog/dto"
	"api/internal/platform/editing"
	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
	"gorm.io/datatypes"
)

// decodeMap / decodeStrings render stored JSONB documents for the wire; the
// engine wrote them, so a decode failure is a programming error — the view
// then carries a nil field rather than failing the whole read.
func decodeMap(raw datatypes.JSON) (map[string]any, error) {
	var m map[string]any
	err := json.Unmarshal(raw, &m)
	return m, err
}

func decodeStrings(raw datatypes.JSON) []string {
	var s []string
	_ = json.Unmarshal(raw, &s)
	return s
}

// The editing-engine S2S face (E0, charter ruling 6): proposal CRUD + amend
// + merge/decline/withdraw, the revision log with any-two-versions diff and
// revert, and the edit-schema projection. Registered under /api/v1/catalog
// so the existing path-scoped S2SAuth (Basic client credentials) gates it;
// WRITE paths additionally enforce the client's catalog_site binding against
// the proposal's site — the same tenancy line as the claim path. The public
// /v1 face exposes NONE of this (E4 decides that separately).
//
// The actor (end user) is ASSERTED by the product backend in the request
// body (the community S2S convention): roles evaluate through the entity
// FAMILY's own perm vocabulary (E3a ruling 1 — the resolver map below),
// trust_tier feeds the trusted policy rules.

// PermResolvers routes an entity family to the permission vocabulary its
// asserted roles evaluate through (E3a ruling 1). The face hardcodes no
// family name — assembly points register whatever families they serve,
// exactly like the EntityTypeSpec registrations; a family absent from the
// map fails closed (every perm-gated rule denies).
type PermResolvers map[string]*authz.Resolver

// EditServer holds the editing operations' dependencies.
type EditServer struct {
	engine *editing.Engine
	perms  PermResolvers
}

// SetupEdit registers the editing operations on the S2S Huma API built by
// Setup. Callable with a nil engine/resolver map for spec export (handlers
// never run).
func SetupEdit(api huma.API, engine *editing.Engine, perms PermResolvers) {
	s := &EditServer{engine: engine, perms: perms}
	tags := []string{"catalog-edit"}

	huma.Register(api, huma.Operation{
		OperationID: "createEditProposal", Method: http.MethodPost, Path: "/api/v1/catalog/edit/proposals",
		Summary: "File an edit proposal (automerges into a direct edit when policy allows)", Tags: tags,
	}, s.create)
	huma.Register(api, huma.Operation{
		OperationID: "listEditProposals", Method: http.MethodGet, Path: "/api/v1/catalog/edit/proposals",
		Summary: "List edit proposals (review queue)", Tags: tags,
	}, s.list)
	huma.Register(api, huma.Operation{
		OperationID: "getEditProposal", Method: http.MethodGet, Path: "/api/v1/catalog/edit/proposals/{id}",
		Summary: "Read one proposal with amendments and the effective patch", Tags: tags,
	}, s.get)
	huma.Register(api, huma.Operation{
		OperationID: "amendEditProposal", Method: http.MethodPost, Path: "/api/v1/catalog/edit/proposals/{id}/amendments",
		Summary: "Amend an open proposal (set/unset fields; requires the review rule)", Tags: tags,
	}, s.amend)
	huma.Register(api, huma.Operation{
		OperationID: "mergeEditProposal", Method: http.MethodPost, Path: "/api/v1/catalog/edit/proposals/{id}/merge",
		Summary: "Merge an open proposal (per-field rebase; 409 lists conflicts)", Tags: tags,
	}, s.merge)
	huma.Register(api, huma.Operation{
		OperationID: "declineEditProposal", Method: http.MethodPost, Path: "/api/v1/catalog/edit/proposals/{id}/decline",
		Summary: "Decline an open proposal with a reason", Tags: tags,
	}, s.decline)
	huma.Register(api, huma.Operation{
		OperationID: "withdrawEditProposal", Method: http.MethodPost, Path: "/api/v1/catalog/edit/proposals/{id}/withdraw",
		Summary: "Withdraw one's own open proposal", Tags: tags,
	}, s.withdraw)
	huma.Register(api, huma.Operation{
		OperationID: "listEditRevisions", Method: http.MethodGet, Path: "/api/v1/catalog/edit/revisions",
		Summary: "An entity's revision log, newest-first", Tags: tags,
	}, s.revisions)
	huma.Register(api, huma.Operation{
		OperationID: "diffEditRevisions", Method: http.MethodGet, Path: "/api/v1/catalog/edit/diff",
		Summary: "Field-level diff between any two revisions", Tags: tags,
	}, s.diff)
	huma.Register(api, huma.Operation{
		OperationID: "revertEditEntity", Method: http.MethodPost, Path: "/api/v1/catalog/edit/revert",
		Summary: "Restore an entity to a historical revision (a new revision; history kept)", Tags: tags,
	}, s.revert)
	huma.Register(api, huma.Operation{
		OperationID: "getEditSchema", Method: http.MethodGet, Path: "/api/v1/catalog/edit/schema/{entity_type}",
		Summary: "Field schema + the caller's evaluated field-level capabilities", Tags: tags,
	}, s.schema)
	huma.Register(api, huma.Operation{
		OperationID: "getEditSnapshot", Method: http.MethodGet, Path: "/api/v1/catalog/edit/snapshot",
		Summary: "The entity's current registered-field values (the BFF editor's bootstrap read)", Tags: tags,
	}, s.snapshot)
}

// familyOf derives the entity family from a registered entity type — its
// first dotted segment (the wire carries no family; E0 deviation 8). An
// unknown type resolves to a family absent from the resolver map, which
// fails closed.
func familyOf(entityType string) string {
	family, _, _ := strings.Cut(entityType, ".")
	return family
}

// policyCtx builds the engine policy context from an asserted actor: roles
// resolve through the FAMILY's perm vocabulary (fail-closed when the family
// registered no resolver), trust tier passes through.
func (s *EditServer) policyCtx(actor dto.EditActor, site, family string) editing.PolicyContext {
	roles := actor.Roles
	resolver := s.perms[family]
	return editing.PolicyContext{
		UserID: actor.UserID, Site: site, TrustTier: actor.TrustTier,
		HasPerm: func(key string) bool {
			if resolver == nil {
				return false
			}
			return resolver.Can(roles, authz.Permission(key))
		},
	}
}

// editErr maps engine errors onto the house envelope.
func editErr(err error) error {
	var (
		unknownField *editing.UnknownFieldError
		lockedField  *editing.LockedFieldError
		validation   *editing.ValidationError
		permission   *editing.PermissionError
		conflict     *editing.ConflictError
	)
	switch {
	case stderrors.Is(err, editing.ErrProposalNotFound),
		stderrors.Is(err, editing.ErrRevisionNotFound),
		stderrors.Is(err, editing.ErrEntityNotFound),
		stderrors.Is(err, editing.ErrUnknownEntityType):
		return apiErrMsg(http.StatusNotFound, errors.ErrNotFound, err.Error())
	case stderrors.Is(err, editing.ErrEmptyPatch),
		stderrors.Is(err, editing.ErrEmptyDelta),
		stderrors.Is(err, editing.ErrNoEffectiveChanges):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed, err.Error())
	case stderrors.As(err, &unknownField), stderrors.As(err, &lockedField), stderrors.As(err, &validation):
		return apiErrMsg(http.StatusUnprocessableEntity, errors.ErrValidationFailed, err.Error())
	case stderrors.As(err, &permission), stderrors.Is(err, editing.ErrNotProposer):
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden, err.Error())
	case stderrors.As(err, &conflict):
		return apiErrData(http.StatusConflict, errors.ErrOperationFailed, err.Error(),
			dto.EditConflictInfo{Conflicts: conflict.Keys})
	case stderrors.Is(err, editing.ErrNotOpen):
		return apiErrMsg(http.StatusConflict, errors.ErrOperationFailed, err.Error())
	}
	slog.Error("catalog edit", "err", err)
	return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
}

// ---- views -----------------------------------------------------------------

func proposalView(p *editing.Proposal) dto.EditProposalView {
	v := dto.EditProposalView{
		ID: p.ID, EntityFamily: p.EntityFamily, EntityType: p.EntityType, EntityID: p.EntityID,
		BaseRevisionSeq: p.BaseRevisionSeq, ProposerUID: p.ProposerUID,
		Note: p.Note, Site: p.Site, Status: editing.StatusName[p.Status],
		DecidedByUID: p.DecidedByUID, DecidedAt: p.DecidedAt, DecisionNote: p.DecisionNote,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	v.Patch, _ = decodeMap(p.Patch)
	return v
}

func revisionView(r *editing.Revision) dto.EditRevisionView {
	v := dto.EditRevisionView{
		ID: r.ID, EntityFamily: r.EntityFamily, EntityType: r.EntityType, EntityID: r.EntityID,
		Seq: r.Seq, Action: editing.ActionName[r.Action],
		ActorUID: r.ActorUID, AmenderUID: r.AmenderUID, ProposalID: r.ProposalID,
		Site: r.Site, CreatedAt: r.CreatedAt,
	}
	v.Snapshot, _ = decodeMap(r.Snapshot)
	v.ChangedFields = decodeStrings(r.ChangedFields)
	return v
}

func amendmentViews(items []editing.ProposalAmendment) []dto.EditAmendmentView {
	out := make([]dto.EditAmendmentView, 0, len(items))
	for i := range items {
		a := &items[i]
		var d editing.Delta
		_ = json.Unmarshal(a.PatchDelta, &d)
		out = append(out, dto.EditAmendmentView{
			ID: a.ID, Seq: a.Seq, Set: d.Set, Unset: d.Unset,
			AmenderUID: a.AmenderUID, Note: a.Note, CreatedAt: a.CreatedAt,
		})
	}
	return out
}

// ---- operations ------------------------------------------------------------

type editCreateInput struct {
	Body dto.EditProposalCreateRequest
}

type editCreateOutput struct {
	Body Envelope[dto.EditProposalCreateResponse]
}

func (s *EditServer) create(ctx context.Context, in *editCreateInput) (*editCreateOutput, error) {
	if he := enforceSiteBinding(clientFromCtx(ctx), in.Body.Site); he != nil {
		return nil, he
	}
	prop, rev, err := s.engine.CreateProposal(ctx, editing.CreateProposalInput{
		EntityType: in.Body.EntityType, EntityID: in.Body.EntityID,
		Patch: in.Body.Patch, Note: in.Body.Note,
		Actor: s.policyCtx(in.Body.Actor, in.Body.Site, familyOf(in.Body.EntityType)),
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

type editListInput struct {
	EntityType  string `query:"entity_type" doc:"Filter to one entity type"`
	EntityID    int64  `query:"entity_id" doc:"Filter to one entity (requires entity_type)"`
	Site        string `query:"site" doc:"Filter to one tenant"`
	ProposerUID int64  `query:"proposer_uid" doc:"Filter to one proposer (the BFF 'my proposals' face); 0 = all"`
	Status      string `query:"status" enum:",open,merged,declined,withdrawn" doc:"Filter by status; empty = all"`
	Limit       int    `query:"limit" doc:"Page size (max 200, default 50)"`
}

type editListOutput struct {
	Body Envelope[dto.EditProposalListResponse]
}

func (s *EditServer) list(ctx context.Context, in *editListInput) (*editListOutput, error) {
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
	items, err := s.engine.ListProposals(ctx, editing.ProposalFilter{
		EntityType: in.EntityType, EntityID: in.EntityID, Site: in.Site,
		ProposerUID: in.ProposerUID, Status: status, Limit: in.Limit,
	})
	if err != nil {
		return nil, editErr(err)
	}
	resp := dto.EditProposalListResponse{Items: make([]dto.EditProposalView, 0, len(items))}
	for i := range items {
		resp.Items = append(resp.Items, proposalView(&items[i]))
	}
	return &editListOutput{Body: okEnvelope(resp)}, nil
}

type editGetInput struct {
	ID int64 `path:"id"`
}

type editGetOutput struct {
	Body Envelope[dto.EditProposalView]
}

func (s *EditServer) get(ctx context.Context, in *editGetInput) (*editGetOutput, error) {
	prop, amendments, eff, err := s.engine.GetProposal(ctx, in.ID)
	if err != nil {
		return nil, editErr(err)
	}
	view := proposalView(prop)
	view.EffectivePatch = eff
	view.Amendments = amendmentViews(amendments)
	return &editGetOutput{Body: okEnvelope(view)}, nil
}

type editAmendInput struct {
	ID   int64 `path:"id"`
	Body dto.EditAmendRequest
}

type editAmendOutput struct {
	Body Envelope[dto.EditAmendmentView]
}

func (s *EditServer) amend(ctx context.Context, in *editAmendInput) (*editAmendOutput, error) {
	prop, err := s.proposalForWrite(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	amendment, err := s.engine.AmendProposal(ctx, in.ID, editing.AmendInput{
		Set: in.Body.Set, Unset: in.Body.Unset, Note: in.Body.Note,
		Actor: s.policyCtx(in.Body.Actor, prop.Site, prop.EntityFamily),
	})
	if err != nil {
		return nil, editErr(err)
	}
	views := amendmentViews([]editing.ProposalAmendment{*amendment})
	return &editAmendOutput{Body: okEnvelope(views[0])}, nil
}

type editDecisionInput struct {
	ID   int64 `path:"id"`
	Body dto.EditDecisionRequest
}

type editMergeOutput struct {
	Body Envelope[dto.EditRevisionView]
}

func (s *EditServer) merge(ctx context.Context, in *editDecisionInput) (*editMergeOutput, error) {
	prop, err := s.proposalForWrite(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	rev, err := s.engine.MergeProposal(ctx, in.ID, s.policyCtx(in.Body.Actor, prop.Site, prop.EntityFamily), in.Body.Note)
	if err != nil {
		return nil, editErr(err)
	}
	return &editMergeOutput{Body: okEnvelope(revisionView(rev))}, nil
}

type editCloseOutput struct {
	Body Envelope[dto.EditProposalView]
}

func (s *EditServer) decline(ctx context.Context, in *editDecisionInput) (*editCloseOutput, error) {
	prop, err := s.proposalForWrite(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := s.engine.DeclineProposal(ctx, in.ID, s.policyCtx(in.Body.Actor, prop.Site, prop.EntityFamily), in.Body.Note); err != nil {
		return nil, editErr(err)
	}
	return s.closedView(ctx, in.ID)
}

type editWithdrawInput struct {
	ID   int64 `path:"id"`
	Body dto.EditWithdrawRequest
}

func (s *EditServer) withdraw(ctx context.Context, in *editWithdrawInput) (*editCloseOutput, error) {
	prop, err := s.proposalForWrite(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := s.engine.WithdrawProposal(ctx, in.ID, s.policyCtx(in.Body.Actor, prop.Site, prop.EntityFamily)); err != nil {
		return nil, editErr(err)
	}
	return s.closedView(ctx, in.ID)
}

// proposalForWrite loads a proposal and enforces the S2S client's site
// binding against the proposal's tenant (write paths only).
func (s *EditServer) proposalForWrite(ctx context.Context, id int64) (*editing.Proposal, error) {
	prop, _, _, err := s.engine.GetProposal(ctx, id)
	if err != nil {
		return nil, editErr(err)
	}
	if he := enforceSiteBinding(clientFromCtx(ctx), prop.Site); he != nil {
		return nil, he
	}
	return prop, nil
}

func (s *EditServer) closedView(ctx context.Context, id int64) (*editCloseOutput, error) {
	prop, _, _, err := s.engine.GetProposal(ctx, id)
	if err != nil {
		return nil, editErr(err)
	}
	return &editCloseOutput{Body: okEnvelope(proposalView(prop))}, nil
}

type editRevisionsInput struct {
	EntityType string `query:"entity_type" minLength:"1" doc:"Registered entity type, e.g. catalog.work"`
	EntityID   int64  `query:"entity_id" minimum:"1"`
	Limit      int    `query:"limit" doc:"Page size (max 200, default 50)"`
}

type editRevisionsOutput struct {
	Body Envelope[dto.EditRevisionListResponse]
}

func (s *EditServer) revisions(ctx context.Context, in *editRevisionsInput) (*editRevisionsOutput, error) {
	items, err := s.engine.ListRevisions(ctx, in.EntityType, in.EntityID, in.Limit)
	if err != nil {
		return nil, editErr(err)
	}
	resp := dto.EditRevisionListResponse{Items: make([]dto.EditRevisionView, 0, len(items))}
	for i := range items {
		resp.Items = append(resp.Items, revisionView(&items[i]))
	}
	return &editRevisionsOutput{Body: okEnvelope(resp)}, nil
}

type editDiffInput struct {
	EntityType string `query:"entity_type" minLength:"1"`
	EntityID   int64  `query:"entity_id" minimum:"1"`
	FromSeq    int    `query:"from_seq" minimum:"1"`
	ToSeq      int    `query:"to_seq" minimum:"1"`
}

type editDiffOutput struct {
	Body Envelope[dto.EditDiffResponse]
}

func (s *EditServer) diff(ctx context.Context, in *editDiffInput) (*editDiffOutput, error) {
	diffs, err := s.engine.Diff(ctx, in.EntityType, in.EntityID, in.FromSeq, in.ToSeq)
	if err != nil {
		return nil, editErr(err)
	}
	resp := dto.EditDiffResponse{
		FromSeq: in.FromSeq, ToSeq: in.ToSeq,
		Fields: make([]dto.EditFieldDiffView, 0, len(diffs)),
	}
	for _, d := range diffs {
		resp.Fields = append(resp.Fields, dto.EditFieldDiffView{
			Key: d.Key, Kind: string(d.Kind), DiffHint: d.DiffHint, From: d.From, To: d.To,
		})
	}
	return &editDiffOutput{Body: okEnvelope(resp)}, nil
}

type editRevertInput struct {
	Body dto.EditRevertRequest
}

type editRevertOutput struct {
	Body Envelope[dto.EditRevertResponse]
}

func (s *EditServer) revert(ctx context.Context, in *editRevertInput) (*editRevertOutput, error) {
	if he := enforceSiteBinding(clientFromCtx(ctx), in.Body.Site); he != nil {
		return nil, he
	}
	prop, rev, err := s.engine.Revert(ctx, editing.RevertInput{
		EntityType: in.Body.EntityType, EntityID: in.Body.EntityID, ToSeq: in.Body.ToSeq,
		Note: in.Body.Note, Actor: s.policyCtx(in.Body.Actor, in.Body.Site, familyOf(in.Body.EntityType)),
	})
	if err != nil {
		return nil, editErr(err)
	}
	return &editRevertOutput{Body: okEnvelope(dto.EditRevertResponse{
		Proposal: proposalView(prop), Revision: revisionView(rev),
	})}, nil
}

type editSnapshotInput struct {
	EntityType string `query:"entity_type" minLength:"1" doc:"Registered entity type, e.g. galgame.game"`
	EntityID   int64  `query:"entity_id" minimum:"1"`
}

type editSnapshotOutput struct {
	Body Envelope[dto.EditSnapshotResponse]
}

func (s *EditServer) snapshot(ctx context.Context, in *editSnapshotInput) (*editSnapshotOutput, error) {
	values, err := s.engine.CurrentSnapshot(ctx, in.EntityType, in.EntityID)
	if err != nil {
		return nil, editErr(err)
	}
	return &editSnapshotOutput{Body: okEnvelope(dto.EditSnapshotResponse{
		EntityType: in.EntityType, EntityID: in.EntityID, Values: values,
	})}, nil
}

type editSchemaInput struct {
	EntityType string `path:"entity_type" doc:"Registered entity type, e.g. catalog.work"`
	EntityID   int64  `query:"entity_id" doc:"Entity-aware projection: owner automerge evaluates against this entity (0 = type-level, owner projects false)"`
	Site       string `query:"site" doc:"Tenant whose policy overlay applies"`
	UserID     int64  `query:"user_id" doc:"Asserted end-user id (0 = anonymous projection)"`
	Roles      string `query:"roles" doc:"Comma-separated asserted roles"`
	TrustTier  int16  `query:"trust_tier" minimum:"0" maximum:"4" doc:"Asserted trust tier"`
}

type editSchemaOutput struct {
	Body Envelope[dto.EditSchemaResponse]
}

func (s *EditServer) schema(ctx context.Context, in *editSchemaInput) (*editSchemaOutput, error) {
	actor := dto.EditActor{UserID: in.UserID, TrustTier: in.TrustTier}
	if in.Roles != "" {
		actor.Roles = strings.Split(in.Roles, ",")
	}
	fields, err := s.engine.SchemaProjection(ctx, in.EntityType, in.EntityID, s.policyCtx(actor, in.Site, familyOf(in.EntityType)))
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
