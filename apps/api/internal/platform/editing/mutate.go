package editing

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This file is the single merge path (lifetime pillar 1): every write that
// lands on an entity — reviewer merge, direct-edit automerge sugar, revert —
// flows through mergeLocked, so revision/audit semantics can never fork.
//
// Transaction posture: the ENGINE transaction (engine pool) covers proposal
// state + revision append; field Applies run inside a transaction on the
// FAMILY's own pool (spec.Txn), opened as the LAST step inside the engine
// transaction. An Apply failure rolls everything back. The residual window —
// the family tx commits but the engine commit itself fails — cannot be
// closed while entity bodies may live on other pools/databases (charter
// ruling 2 accepts this); the revision unique index keeps history itself
// consistent under every race.

// CreateProposalInput carries one proposal creation. Actor.Site is both the
// proposal's tenant and the policy-overlay key.
type CreateProposalInput struct {
	EntityType string
	EntityID   int64
	Patch      map[string]any
	Note       string
	Actor      PolicyContext
}

// CreateProposal validates and files a proposal. When every patched field's
// automerge rule passes for the proposer, the proposal is created and merged
// atomically (the direct-edit sugar) and the produced revision is returned;
// otherwise the proposal stays open and the revision is nil.
func (e *Engine) CreateProposal(ctx context.Context, in CreateProposalInput) (*Proposal, *Revision, error) {
	spec, err := e.resolveSpec(in.EntityType)
	if err != nil {
		return nil, nil, err
	}
	if len(in.Patch) == 0 {
		return nil, nil, ErrEmptyPatch
	}
	pols := make(map[string]Policy, len(in.Patch))
	needOwner := false
	for _, key := range sortedKeys(in.Patch) {
		f, err := spec.fieldForWrite(key)
		if err != nil {
			return nil, nil, err
		}
		pol := spec.EffectivePolicy(key, in.Actor.Site)
		if pol.Propose == ProposeLocked {
			return nil, nil, &LockedFieldError{Key: key}
		}
		if !pol.AllowsPropose(in.Actor) {
			return nil, nil, &PermissionError{Key: key, Action: "propose"}
		}
		if err := f.Validate(in.Patch[key]); err != nil {
			return nil, nil, &ValidationError{Key: key, Reason: err.Error()}
		}
		pols[key] = pol
		if pol.Automerge == AutomergeOwner {
			needOwner = true
		}
	}
	// The owner site is resolved AT MOST ONCE per create, and only when some
	// patched field carries the owner rule. A hook error fails the create
	// (never a silent downgrade to an open proposal — the caller retries).
	owner, err := e.ownerSite(ctx, spec, in.EntityID, needOwner)
	if err != nil {
		return nil, nil, err
	}
	automerge := true
	for _, pol := range pols {
		if !pol.allowsAutomergeWithOwner(in.Actor, owner) {
			automerge = false
			break
		}
	}
	// The entity must exist (and this read anchors nothing else — the merge
	// path re-reads its own base snapshot).
	if _, err := spec.LoadSnapshot(ctx, in.EntityID); err != nil {
		return nil, nil, err
	}
	baseSeq, err := maxRevisionSeq(e.db.WithContext(ctx), spec, in.EntityID)
	if err != nil {
		return nil, nil, err
	}
	rawPatch, err := encodeJSON(in.Patch)
	if err != nil {
		return nil, nil, err
	}
	prop := &Proposal{
		EntityFamily: spec.Family, EntityType: spec.Type, EntityID: in.EntityID,
		BaseRevisionSeq: baseSeq, Patch: rawPatch,
		ProposerUID: in.Actor.UserID, Note: in.Note, Site: in.Actor.Site, Status: StatusOpen,
	}
	if !automerge {
		if err := e.db.WithContext(ctx).Create(prop).Error; err != nil {
			return nil, nil, err
		}
		return prop, nil, nil
	}
	var rev *Revision
	err = e.db.WithContext(ctx).Transaction(func(etx *gorm.DB) error {
		if err := etx.Create(prop).Error; err != nil {
			return err
		}
		r, err := e.mergeLocked(ctx, etx, spec, prop, in.Patch, ActionDirect, nil, in.Actor.UserID, "")
		if err != nil {
			return err
		}
		rev = r
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return prop, rev, nil
}

// AmendInput is one maintainer edit on an open proposal.
type AmendInput struct {
	Set   map[string]any
	Unset []string
	Note  string
	Actor PolicyContext
}

// AmendProposal appends a patch delta (doc 21 §2.3): set = correct/add a
// field, unset = reject a field. Amend requires the REVIEW rule on every
// touched field (charter ruling: amend 权限 = review 权); policies evaluate
// against the PROPOSAL's site.
func (e *Engine) AmendProposal(ctx context.Context, proposalID int64, in AmendInput) (*ProposalAmendment, error) {
	if len(in.Set) == 0 && len(in.Unset) == 0 {
		return nil, ErrEmptyDelta
	}
	var amendment *ProposalAmendment
	err := e.db.WithContext(ctx).Transaction(func(etx *gorm.DB) error {
		prop, err := lockProposal(etx, proposalID)
		if err != nil {
			return err
		}
		if prop.Status != StatusOpen {
			return ErrNotOpen
		}
		spec, err := e.resolveSpec(prop.EntityType)
		if err != nil {
			return err
		}
		amendments, err := loadAmendments(etx, proposalID)
		if err != nil {
			return err
		}
		eff, _, err := effectivePatch(prop, amendments)
		if err != nil {
			return err
		}
		unset := make(map[string]struct{}, len(in.Unset))
		for _, key := range in.Unset {
			unset[key] = struct{}{}
		}
		for _, key := range sortedKeys(in.Set) {
			if _, both := unset[key]; both {
				return &ValidationError{Key: key, Reason: "key is both set and unset"}
			}
			f, err := spec.fieldForWrite(key)
			if err != nil {
				return err
			}
			pol := spec.EffectivePolicy(key, prop.Site)
			// An amendment must not smuggle in a field nobody may propose.
			if pol.Propose == ProposeLocked {
				return &LockedFieldError{Key: key}
			}
			if !pol.AllowsReview(in.Actor) {
				return &PermissionError{Key: key, Action: "review"}
			}
			if err := f.Validate(in.Set[key]); err != nil {
				return &ValidationError{Key: key, Reason: err.Error()}
			}
		}
		for _, key := range in.Unset {
			if _, ok := spec.Field(key); !ok {
				return &UnknownFieldError{Key: key}
			}
			if _, present := eff[key]; !present {
				return &ValidationError{Key: key, Reason: "key is not in the effective patch"}
			}
			if !spec.EffectivePolicy(key, prop.Site).AllowsReview(in.Actor) {
				return &PermissionError{Key: key, Action: "review"}
			}
		}
		rawDelta, err := encodeJSON(Delta{Set: in.Set, Unset: in.Unset})
		if err != nil {
			return err
		}
		amendment = &ProposalAmendment{
			ProposalID: proposalID, Seq: len(amendments) + 1,
			PatchDelta: rawDelta, AmenderUID: in.Actor.UserID, Note: in.Note,
		}
		// The (proposal_id, seq) unique index makes concurrent amenders
		// collide; the proposal row lock above already serializes them.
		return etx.Create(amendment).Error
	})
	if err != nil {
		return nil, err
	}
	return amendment, nil
}

// MergeProposal merges an open proposal: per-field rebase against the
// revisions after its base (silently fast-forwarding disjoint fields,
// rejecting un-readjudicated conflicts with the field list), then the single
// merge path. The reviewer needs the review rule on every effective field.
func (e *Engine) MergeProposal(ctx context.Context, proposalID int64, actor PolicyContext, note string) (*Revision, error) {
	var rev *Revision
	err := e.db.WithContext(ctx).Transaction(func(etx *gorm.DB) error {
		prop, err := lockProposal(etx, proposalID)
		if err != nil {
			return err
		}
		if prop.Status != StatusOpen {
			return ErrNotOpen
		}
		spec, err := e.resolveSpec(prop.EntityType)
		if err != nil {
			return err
		}
		amendments, err := loadAmendments(etx, proposalID)
		if err != nil {
			return err
		}
		eff, amended, err := effectivePatch(prop, amendments)
		if err != nil {
			return err
		}
		if len(eff) == 0 {
			return ErrEmptyPatch
		}
		for _, key := range sortedKeys(eff) {
			f, err := spec.fieldForWrite(key)
			if err != nil {
				return err
			}
			if !spec.EffectivePolicy(key, prop.Site).AllowsReview(actor) {
				return &PermissionError{Key: key, Action: "review"}
			}
			if err := f.Validate(eff[key]); err != nil {
				return &ValidationError{Key: key, Reason: err.Error()}
			}
		}
		// Rebase (doc 21 §2.3): fields changed since base that no amendment
		// re-adjudicated are conflicts; everything else fast-forwards.
		drifted, err := driftedFields(etx, spec, prop.EntityID, prop.BaseRevisionSeq)
		if err != nil {
			return err
		}
		var conflicts []string
		for _, key := range sortedKeys(eff) {
			if _, moved := drifted[key]; !moved {
				continue
			}
			if _, ok := amended[key]; !ok {
				conflicts = append(conflicts, key)
			}
		}
		if len(conflicts) > 0 {
			return &ConflictError{Keys: conflicts}
		}
		var amender *int64
		if n := len(amendments); n > 0 {
			amender = &amendments[n-1].AmenderUID
		}
		rev, err = e.mergeLocked(ctx, etx, spec, prop, eff, ActionMerged, amender, actor.UserID, note)
		return err
	})
	if err != nil {
		return nil, err
	}
	return rev, nil
}

// DeclineProposal closes an open proposal without landing it. Requires the
// review rule on every effective-patch field (original fields when the
// amendments emptied the patch). The reason lands in decision_note (parity:
// 审核队列与拒绝理由消息).
func (e *Engine) DeclineProposal(ctx context.Context, proposalID int64, actor PolicyContext, note string) error {
	return e.db.WithContext(ctx).Transaction(func(etx *gorm.DB) error {
		prop, err := lockProposal(etx, proposalID)
		if err != nil {
			return err
		}
		if prop.Status != StatusOpen {
			return ErrNotOpen
		}
		spec, err := e.resolveSpec(prop.EntityType)
		if err != nil {
			return err
		}
		amendments, err := loadAmendments(etx, proposalID)
		if err != nil {
			return err
		}
		eff, _, err := effectivePatch(prop, amendments)
		if err != nil {
			return err
		}
		keys := sortedKeys(eff)
		if len(keys) == 0 {
			orig, err := decodeObject(prop.Patch)
			if err != nil {
				return err
			}
			keys = sortedKeys(orig)
		}
		for _, key := range keys {
			if !spec.EffectivePolicy(key, prop.Site).AllowsReview(actor) {
				return &PermissionError{Key: key, Action: "review"}
			}
		}
		return closeProposal(etx, prop.ID, StatusDeclined, actor.UserID, note)
	})
}

// WithdrawProposal lets the PROPOSER close their own open proposal.
func (e *Engine) WithdrawProposal(ctx context.Context, proposalID int64, actor PolicyContext) error {
	return e.db.WithContext(ctx).Transaction(func(etx *gorm.DB) error {
		prop, err := lockProposal(etx, proposalID)
		if err != nil {
			return err
		}
		if prop.Status != StatusOpen {
			return ErrNotOpen
		}
		if prop.ProposerUID != actor.UserID {
			return ErrNotProposer
		}
		return closeProposal(etx, prop.ID, StatusWithdrawn, actor.UserID, "")
	})
}

// mergeLocked lands an effective patch: no-op fields are filtered against a
// fresh base snapshot (changed_fields precision), the revision is appended
// (snapshot = base ⊕ changes, double signature), the proposal is closed as
// merged, and the field Applies run on the family pool. Callers hold the
// proposal row lock inside etx.
func (e *Engine) mergeLocked(
	ctx context.Context, etx *gorm.DB, spec *EntityTypeSpec, prop *Proposal,
	eff map[string]any, action int16, amenderUID *int64, decidedBy int64, decisionNote string,
) (*Revision, error) {
	base, err := spec.LoadSnapshot(ctx, prop.EntityID)
	if err != nil {
		return nil, err
	}
	changed := make([]string, 0, len(eff))
	for _, key := range sortedKeys(eff) {
		if !jsonValueEqual(base[key], eff[key]) {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		return nil, ErrNoEffectiveChanges
	}
	snapshot := make(map[string]any, len(base))
	for k, v := range base {
		snapshot[k] = v
	}
	for _, key := range changed {
		snapshot[key] = eff[key]
	}
	seq, err := maxRevisionSeq(etx, spec, prop.EntityID)
	if err != nil {
		return nil, err
	}
	rawChanged, err := encodeJSON(changed)
	if err != nil {
		return nil, err
	}
	rawSnapshot, err := encodeJSON(snapshot)
	if err != nil {
		return nil, err
	}
	rev := &Revision{
		EntityFamily: spec.Family, EntityType: spec.Type, EntityID: prop.EntityID,
		Seq: seq + 1, Action: action,
		ChangedFields: rawChanged, Snapshot: rawSnapshot,
		ActorUID: prop.ProposerUID, AmenderUID: amenderUID, ProposalID: &prop.ID,
		Site: prop.Site,
	}
	// The (entity_ref, seq) unique index turns a concurrent merge on the
	// same entity into a hard error instead of forked history.
	if err := etx.Create(rev).Error; err != nil {
		return nil, err
	}
	if err := closeProposal(etx, prop.ID, StatusMerged, decidedBy, decisionNote); err != nil {
		return nil, err
	}
	// Mirror the DB close onto the caller's struct so every path returns the
	// post-merge state.
	now := time.Now()
	prop.Status = StatusMerged
	prop.DecidedByUID = &decidedBy
	prop.DecidedAt = &now
	prop.DecisionNote = decisionNote
	// Family-pool transaction LAST: its failure rolls the engine tx back.
	err = spec.Txn(ctx, func(atx *gorm.DB) error {
		for _, key := range changed {
			f, _ := spec.Field(key)
			if err := f.Apply(ctx, atx, prop.EntityID, eff[key]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rev, nil
}

func lockProposal(etx *gorm.DB, id int64) (*Proposal, error) {
	var p Proposal
	err := etx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProposalNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func closeProposal(etx *gorm.DB, id int64, status int16, decidedBy int64, note string) error {
	now := time.Now()
	return etx.Model(&Proposal{}).Where("id = ?", id).Updates(map[string]any{
		"status": status, "decided_by_uid": decidedBy, "decided_at": now, "decision_note": note,
	}).Error
}

// driftedFields unions the changed_fields of every revision after baseSeq.
func driftedFields(etx *gorm.DB, spec *EntityTypeSpec, entityID int64, baseSeq int) (map[string]struct{}, error) {
	var revs []Revision
	if err := etx.Select("changed_fields").
		Where("entity_family = ? AND entity_type = ? AND entity_id = ? AND seq > ?",
			spec.Family, spec.Type, entityID, baseSeq).
		Find(&revs).Error; err != nil {
		return nil, err
	}
	drifted := make(map[string]struct{})
	for i := range revs {
		keys, err := decodeKeys(revs[i].ChangedFields)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			drifted[k] = struct{}{}
		}
	}
	return drifted, nil
}
