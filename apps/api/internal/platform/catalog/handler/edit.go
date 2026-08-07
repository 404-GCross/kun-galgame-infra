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

// The editing-engine S2S face. Registered under /api/v1/catalog so the existing
// path-scoped S2SAuth (Basic client credentials) gates it. The public /v1 face
// exposes NONE of this.
//
// Wave 181 shrank the face to six ops by moving everything a HUMAN performs to
// the user-token plane (user_edit.go), leaving create / withdraw / schema here
// for backends that authenticate their own user. Wave 185 retires those three
// too: letmoe migrated, and neither the cross-repo sweep nor 48h of production
// logs found another caller — so the last place a body could ASSERT "I am user
// N" is closed rather than deprecated in place.
//
// What is left is the READ plane, and it needs no actor: the proposal LIST,
// whose proposer_uid names the person being looked at rather than the caller
// claiming to be them, and the revision log + diff, which are public version
// history and belong to nobody. The face therefore no longer has a write path,
// which is why no operation here consults the client's catalog_site binding
// any more.

// PermResolvers routes an entity family to the permission vocabulary its
// asserted roles evaluate through (E3a ruling 1). The face hardcodes no
// family name — assembly points register whatever families they serve,
// exactly like the EntityTypeSpec registrations; a family absent from the
// map fails closed (every perm-gated rule denies).
type PermResolvers map[string]authz.Checker

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
		OperationID: "listEditProposals", Method: http.MethodGet, Path: "/api/v1/catalog/edit/proposals",
		Summary: "List edit proposals (review queue)", Tags: tags,
	}, s.list)
	huma.Register(api, huma.Operation{
		OperationID: "listEditRevisions", Method: http.MethodGet, Path: "/api/v1/catalog/edit/revisions",
		Summary: "An entity's revision log, newest-first", Tags: tags,
	}, s.revisions)
	huma.Register(api, huma.Operation{
		OperationID: "diffEditRevisions", Method: http.MethodGet, Path: "/api/v1/catalog/edit/diff",
		Summary: "Field-level diff between any two revisions", Tags: tags,
	}, s.diff)
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
		IsEntityOwner: actor.IsEntityOwner,
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
	// The wave-155 mirror gate used to take the first case here, 409ing an
	// otherwise valid patch on a facet a duty-chain step still owned. Wave 161
	// retired those steps, so the gate and its case are gone: every registered
	// field now has exactly one persistent writer, which is the state the gate
	// existed to hold the line for.
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
	// Migrated rows (E2) carry honest provenance: the original action word
	// and the old-wire note/minor flag out of legacy_meta. New-era rows have
	// neither — the engine never writes these columns.
	if r.LegacyAction != nil {
		v.LegacyAction = *r.LegacyAction
	}
	v.LegacyID = r.LegacyID
	if len(r.LegacyMeta) > 0 {
		var meta struct {
			Note    string `json:"note"`
			IsMinor bool   `json:"is_minor"`
		}
		if json.Unmarshal(r.LegacyMeta, &meta) == nil {
			v.LegacyNote = meta.Note
			v.LegacyMinor = meta.IsMinor
		}
	}
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
	items, total, err := s.engine.ListProposalsWithTotal(ctx, editing.ProposalFilter{
		EntityType: in.EntityType, EntityID: in.EntityID, Site: in.Site,
		ProposerUID: in.ProposerUID, Status: status, Limit: in.Limit,
	})
	if err != nil {
		return nil, editErr(err)
	}
	resp := dto.EditProposalListResponse{Items: make([]dto.EditProposalView, 0, len(items)), Total: total}
	for i := range items {
		resp.Items = append(resp.Items, proposalView(&items[i]))
	}
	return &editListOutput{Body: okEnvelope(resp)}, nil
}

type editGetInput struct {
	ID int64 `path:"id"`
}

// editGetInput and the output shapes below back ops that this face no longer
// registers — they are the user plane's (user_edit.go), declared here so both
// faces answer with byte-identical envelopes. Moving them would only make two
// copies free to drift.
type editGetOutput struct {
	Body Envelope[dto.EditProposalView]
}

type editCreateOutput struct {
	Body Envelope[dto.EditProposalCreateResponse]
}

type editAmendOutput struct {
	Body Envelope[dto.EditAmendmentView]
}

type editMergeOutput struct {
	Body Envelope[dto.EditRevisionView]
}

type editCloseOutput struct {
	Body Envelope[dto.EditProposalView]
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

type editRevertOutput struct {
	Body Envelope[dto.EditRevertResponse]
}

type editSnapshotOutput struct {
	Body Envelope[dto.EditSnapshotResponse]
}

type editSchemaOutput struct {
	Body Envelope[dto.EditSchemaResponse]
}
