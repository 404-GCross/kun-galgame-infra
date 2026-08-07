package handler

import (
	"context"
	"net/http"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/editing"
	"api/pkg/errors"
)

// The editing engine's two READS on the user-token plane (wave 180).
//
// Waves 177-178 moved every human WRITE of the editing engine here. What was
// left over S2S were the two reads a browser-borne editor cannot work without:
// the snapshot it bootstraps its form from, and the proposal list it renders as
// "my edits" or as the review queue. Until this wave the forum had to fetch
// both over the S2S face while asserting a uid it had authenticated — which is
// precisely the assertion this plane exists to delete.
//
// Their two authority answers are deliberately different, and both are written
// down rather than inferred:
//
//   - the SNAPSHOT is a pure projection of an entity's current registered-field
//     values — the same values every public read already renders. It is fenced
//     by the gate chain (a client-bound catalog:edit token) and by NOTHING else:
//     no tenant fence, because there is no tenant-private fact in it, and the
//     read face's doctrine (docs/catalog/01 §4.3) is that reads are cross-site
//     open. Fencing it would only stop a kungal editor from previewing a work
//     letmoe happens to have claimed, while hiding nothing.
//   - the PROPOSAL LIST is two lists behind one path. `mine=true` is the
//     caller's own filing history, which needs no permission beyond being the
//     caller. `mine` absent is the REVIEW QUEUE — other people's open work,
//     with their notes and patches — and so needs the same review authority the
//     verdict ops (amend / merge / decline) need for that entity type. Both are
//     hard-fenced to the token client's catalog_site: `site` is not a parameter
//     here, so no caller can page another tenant's queue.

type userEditSnapshotInput struct {
	EntityType string `query:"entity_type" minLength:"1" doc:"Registered entity type, e.g. catalog.work"`
	EntityID   int64  `query:"entity_id" minimum:"1"`
}

// snapshot returns the entity's current registered-field values — the S2S op's
// response, verbatim, because it is the S2S op's projection: CurrentSnapshot
// takes no actor at all. See the file comment for why no tenant fence applies.
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

// userEditListInput is the S2S list input minus its two identity parameters.
// `site` is gone because the tenant is the token client's binding, and
// `proposer_uid` is gone because the only proposer this face will filter on is
// the token's own user — asked for by `mine`, which is a boolean precisely so
// that it cannot name anybody.
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
	// The S2S list's status vocabulary, resolved the same way. It is spelled out
	// again rather than shared, so that moving one face's enum can never silently
	// move the other's.
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
		// Never a parameter: the tenant is the token client's binding.
		Site:   site,
		Status: status, Limit: in.Limit,
	}
	if in.Mine {
		// Never a parameter either: the only proposer this face filters on.
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

// mayReview answers "is this token a reviewer of this entity type" with the
// EXACT resolution the verdict ops use, rather than a second opinion about it:
// the family's own perm vocabulary through policyCtx, evaluated against the
// spec's own per-field review rules by SchemaProjection. A caller may read the
// queue when at least one field of the type projects can_review — i.e. when
// there is at least one verdict it could actually reach. Inventing a queue-only
// permission key here would have been a second authority to keep in step with
// the first; deriving it from the same projection the editor UI already renders
// keeps them one fact.
//
// entity_id is passed through, so the entity-aware overlays evaluate too: an
// entry's OWNER may page the queue of their own entry (OwnerReview projects
// can_review there and nowhere else), which is the owner-review lane the
// verdict ops already honour.
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
