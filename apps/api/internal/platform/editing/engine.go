package editing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"
)

// Engine is the editing state machine over the engine tables (edit_*) and
// the injected registry. db is the engine pool (catalog DB, charter ruling
// 2); entity bodies are reached only through each spec's own closures.
type Engine struct {
	db  *gorm.DB
	reg *Registry
}

func NewEngine(db *gorm.DB, reg *Registry) *Engine {
	return &Engine{db: db, reg: reg}
}

// Registry exposes the injected registry (schema endpoints project from it).
func (e *Engine) Registry() *Registry { return e.reg }

func (e *Engine) resolveSpec(entityType string) (*EntityTypeSpec, error) {
	spec, ok := e.reg.Type(entityType)
	if !ok {
		return nil, ErrUnknownEntityType
	}
	return spec, nil
}

// afterMerge fires a spec's post-commit OnMerge hook (registry.go) for a
// revision the single write path just committed. Best-effort by construction:
// it runs OUTSIDE the merge transaction, never propagates an error to the
// caller (a failed reindex or contributor write must not undo a landed merge —
// both are recoverable), and recovers from a misbehaving closure so a family
// hook can never crash the request. rev is nil when no merge happened (an open
// proposal that did not automerge), in which case this no-ops.
func (e *Engine) afterMerge(ctx context.Context, rev *Revision) {
	if rev == nil {
		return
	}
	spec, ok := e.reg.Type(rev.EntityType)
	if !ok || spec.OnMerge == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("editing: OnMerge hook panicked",
				"entity_type", rev.EntityType, "entity_id", rev.EntityID, "panic", r)
		}
	}()
	if err := spec.OnMerge(ctx, MergeEvent{
		EntityID: rev.EntityID, ActorUID: rev.ActorUID, AmenderUID: rev.AmenderUID, Action: rev.Action,
	}); err != nil {
		slog.Warn("editing: OnMerge hook failed",
			"entity_type", rev.EntityType, "entity_id", rev.EntityID, "err", err)
	}
}

// ownerSite resolves the entity's owner site through the spec's OwnerSite
// hook when needed is true (some evaluated policy carries the owner rule).
// Register guarantees the hook exists whenever an owner rule is registered.
func (e *Engine) ownerSite(ctx context.Context, spec *EntityTypeSpec, entityID int64, needed bool) (*string, error) {
	if !needed {
		return nil, nil
	}
	owner, err := spec.OwnerSite(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("editing: owner site for %s/%d: %w", spec.Type, entityID, err)
	}
	return owner, nil
}

// ---- reads ----------------------------------------------------------------

// CurrentSnapshot reads the entity's CURRENT registered-field state through
// the spec's LoadSnapshot closure — the same view merge rebases against and
// revert restores from (E3a: the BFF bootstrap's "current values" source; a
// latest-revision snapshot would go stale under system writes that bypass
// the engine, doc 21 §2.6). Read-only, no policy evaluation — the S2S face
// gates access.
func (e *Engine) CurrentSnapshot(ctx context.Context, entityType string, entityID int64) (map[string]any, error) {
	spec, err := e.resolveSpec(entityType)
	if err != nil {
		return nil, err
	}
	return spec.LoadSnapshot(ctx, entityID)
}

// GetProposal loads a proposal, its amendments (seq order), and the computed
// effective patch.
func (e *Engine) GetProposal(ctx context.Context, id int64) (*Proposal, []ProposalAmendment, map[string]any, error) {
	var p Proposal
	if err := e.db.WithContext(ctx).First(&p, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, ErrProposalNotFound
		}
		return nil, nil, nil, err
	}
	amendments, err := loadAmendments(e.db.WithContext(ctx), id)
	if err != nil {
		return nil, nil, nil, err
	}
	eff, _, err := effectivePatch(&p, amendments)
	if err != nil {
		return nil, nil, nil, err
	}
	return &p, amendments, eff, nil
}

// ProposalFilter narrows ListProposals. Zero values mean "no filter" except
// Status, which uses -1 as its no-filter sentinel (0 = open is meaningful).
type ProposalFilter struct {
	EntityType  string
	EntityID    int64
	Site        string
	ProposerUID int64 // 0 = all proposers ("my proposals" BFF face, E1)
	Status      int16 // -1 = all
	Limit       int   // default 50, max 200
}

// ListProposals returns proposals newest-first.
func (e *Engine) ListProposals(ctx context.Context, f ProposalFilter) ([]Proposal, error) {
	q := e.db.WithContext(ctx).Model(&Proposal{})
	if f.EntityType != "" {
		q = q.Where("entity_type = ?", f.EntityType)
	}
	if f.EntityID != 0 {
		q = q.Where("entity_id = ?", f.EntityID)
	}
	if f.Site != "" {
		q = q.Where("site = ?", f.Site)
	}
	if f.ProposerUID != 0 {
		q = q.Where("proposer_uid = ?", f.ProposerUID)
	}
	if f.Status >= 0 {
		q = q.Where("status = ?", f.Status)
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []Proposal
	if err := q.Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// ListRevisions returns an entity's revision log, newest-first.
func (e *Engine) ListRevisions(ctx context.Context, entityType string, entityID int64, limit int) ([]Revision, error) {
	spec, err := e.resolveSpec(entityType)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []Revision
	if err := e.db.WithContext(ctx).
		Where("entity_family = ? AND entity_type = ? AND entity_id = ?", spec.Family, spec.Type, entityID).
		Order("seq DESC").Limit(limit).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// FieldDiff is one field's difference between two revisions, with the
// registry's rendering hints attached (empty for keys that are no longer
// registered — historic snapshots still render, just generically).
type FieldDiff struct {
	Key      string
	Kind     FieldKind
	DiffHint string
	From     any
	To       any
}

// Diff compares any two of an entity's revisions field-by-field (parity
// hard line: any-two-versions diff).
func (e *Engine) Diff(ctx context.Context, entityType string, entityID int64, fromSeq, toSeq int) ([]FieldDiff, error) {
	spec, err := e.resolveSpec(entityType)
	if err != nil {
		return nil, err
	}
	from, err := e.revisionAt(ctx, spec, entityID, fromSeq)
	if err != nil {
		return nil, err
	}
	to, err := e.revisionAt(ctx, spec, entityID, toSeq)
	if err != nil {
		return nil, err
	}
	fromSnap, err := decodeObject(from.Snapshot)
	if err != nil {
		return nil, err
	}
	toSnap, err := decodeObject(to.Snapshot)
	if err != nil {
		return nil, err
	}
	union := make(map[string]struct{}, len(fromSnap)+len(toSnap))
	for k := range fromSnap {
		union[k] = struct{}{}
	}
	for k := range toSnap {
		union[k] = struct{}{}
	}
	var diffs []FieldDiff
	for _, key := range sortedKeys(union) {
		a, b := fromSnap[key], toSnap[key]
		if jsonValueEqual(a, b) {
			continue
		}
		d := FieldDiff{Key: key, From: a, To: b}
		if f, ok := spec.Field(key); ok {
			d.Kind, d.DiffHint = f.Kind, f.DiffHint
		}
		diffs = append(diffs, d)
	}
	return diffs, nil
}

func (e *Engine) revisionAt(ctx context.Context, spec *EntityTypeSpec, entityID int64, seq int) (*Revision, error) {
	var rev Revision
	err := e.db.WithContext(ctx).
		Where("entity_family = ? AND entity_type = ? AND entity_id = ? AND seq = ?",
			spec.Family, spec.Type, entityID, seq).
		First(&rev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRevisionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

// FieldProjection is one field of the edit-schema endpoint: the field's
// shape plus the CALLER's evaluated capabilities — the UI renders exactly
// this and holds zero policy logic (doc 21 §2.7).
type FieldProjection struct {
	Key        string
	Kind       FieldKind
	DiffHint   string
	Deprecated bool
	Locked     bool
	CanPropose bool
	CanReview  bool
	// WouldAutomerge: a proposal by THIS caller would merge instantly (the
	// direct-edit sugar); implies CanPropose.
	WouldAutomerge bool
}

// SchemaProjection evaluates every registered field of an entity type
// against the caller's policy context (site overlay included via pc.Site).
// entityID makes the projection entity-aware: the owner automerge rule is
// evaluated against that entity's owner site (0 = type-level projection —
// owner rules conservatively project WouldAutomerge=false).
func (e *Engine) SchemaProjection(ctx context.Context, entityType string, entityID int64, pc PolicyContext) ([]FieldProjection, error) {
	spec, err := e.resolveSpec(entityType)
	if err != nil {
		return nil, err
	}
	needOwner := false
	if entityID != 0 {
		for i := range spec.Fields {
			if spec.EffectivePolicy(spec.Fields[i].Key, pc.Site).Automerge == AutomergeOwner {
				needOwner = true
				break
			}
		}
	}
	owner, err := e.ownerSite(ctx, spec, entityID, needOwner)
	if err != nil {
		return nil, err
	}
	out := make([]FieldProjection, 0, len(spec.Fields))
	for i := range spec.Fields {
		f := &spec.Fields[i]
		pol := spec.EffectivePolicy(f.Key, pc.Site)
		proj := FieldProjection{
			Key: f.Key, Kind: f.Kind, DiffHint: f.DiffHint, Deprecated: f.Deprecated,
			Locked:    pol.Propose == ProposeLocked,
			CanReview: pol.AllowsReview(pc),
		}
		if !f.Deprecated && !proj.Locked {
			proj.CanPropose = pol.AllowsPropose(pc)
			proj.WouldAutomerge = proj.CanPropose && pol.allowsAutomergeWithOwner(pc, owner)
		}
		out = append(out, proj)
	}
	return out, nil
}

// ---- shared internals ------------------------------------------------------

func loadAmendments(db *gorm.DB, proposalID int64) ([]ProposalAmendment, error) {
	var out []ProposalAmendment
	if err := db.Where("proposal_id = ?", proposalID).Order("seq ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// maxRevisionSeq returns the entity's highest revision seq (0 = none).
func maxRevisionSeq(db *gorm.DB, spec *EntityTypeSpec, entityID int64) (int, error) {
	var seq int
	err := db.Model(&Revision{}).
		Where("entity_family = ? AND entity_type = ? AND entity_id = ?", spec.Family, spec.Type, entityID).
		Select("COALESCE(MAX(seq), 0)").Scan(&seq).Error
	if err != nil {
		return 0, fmt.Errorf("editing: max revision seq: %w", err)
	}
	return seq, nil
}
