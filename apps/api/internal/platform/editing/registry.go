package editing

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

// FieldKind is the value type of a registered field — it drives client-side
// form rendering and diff presentation, never engine behavior (validation is
// the field's Validate closure).
type FieldKind string

const (
	KindText      FieldKind = "text"
	KindI18nMap   FieldKind = "i18nmap"
	KindEnum      FieldKind = "enum"
	KindInt       FieldKind = "int"
	KindDate      FieldKind = "date"
	KindList      FieldKind = "list"
	KindRef       FieldKind = "ref"
	KindImageHash FieldKind = "imagehash"
)

// Diff rendering hints (doc 21 §2.4): text line-by-line, lists item-by-item,
// images side-by-side, scalars inline.
const (
	DiffHintInline = "inline"
	DiffHintLines  = "lines"
	DiffHintItems  = "items"
	DiffHintImage  = "image"
)

// Propose rules (doc 21 §2.5). "perm:<key>" is built with ProposePerm.
const (
	ProposeOpen    = "open"
	ProposeTrusted = "trusted"
	ProposeLocked  = "locked"
)

// Automerge rules. "trusted" automerges when the PROPOSER's trust tier
// reaches TrustedTier; "always" is the direct-edit tier (e.g. a site editing
// works it minted itself — revisions are still recorded).
const (
	AutomergeNever   = "never"
	AutomergeTrusted = "trusted"
	AutomergeAlways  = "always"
)

const permPrefix = "perm:"

// ProposePerm builds a propose rule gated on a permission key.
func ProposePerm(key string) string { return permPrefix + key }

// ReviewPerm builds a review rule gated on a permission key. Review is
// ALWAYS permission-gated — there is no open/trusted review.
func ReviewPerm(key string) string { return permPrefix + key }

// Policy is one field's edit policy triple (doc 21 §2.5). The effective
// policy is the registry default ⊕ per-field override ⊕ site overlay.
type Policy struct {
	Propose   string // open | trusted | locked | perm:<key>
	Review    string // perm:<key>
	Automerge string // never | trusted | always
}

// AllowsPropose evaluates the propose rule for a caller. Locked always
// denies (callers should surface LockedFieldError before this for a 422
// rather than a 403 — locked is a schema property, not a caller property).
func (p Policy) AllowsPropose(pc PolicyContext) bool {
	switch {
	case p.Propose == ProposeOpen:
		return true
	case p.Propose == ProposeTrusted:
		return pc.TrustTier >= TrustedTier
	case strings.HasPrefix(p.Propose, permPrefix):
		return pc.hasPerm(strings.TrimPrefix(p.Propose, permPrefix))
	default: // locked or malformed — fail closed
		return false
	}
}

// AllowsReview evaluates the review rule for a caller.
func (p Policy) AllowsReview(pc PolicyContext) bool {
	if !strings.HasPrefix(p.Review, permPrefix) {
		return false // malformed — fail closed
	}
	return pc.hasPerm(strings.TrimPrefix(p.Review, permPrefix))
}

// AllowsAutomerge evaluates the automerge rule for the PROPOSER.
func (p Policy) AllowsAutomerge(pc PolicyContext) bool {
	switch p.Automerge {
	case AutomergeAlways:
		return true
	case AutomergeTrusted:
		return pc.TrustTier >= TrustedTier
	default:
		return false
	}
}

// ApplyFunc writes one field's new value onto the entity's physical model.
// tx is a transaction on the FAMILY's own DB pool (opened via the spec's
// Txn); the closure is the only key→column translation layer.
type ApplyFunc func(ctx context.Context, tx *gorm.DB, entityID int64, value any) error

// FieldSpec declares one editable field of an entity type (doc 21 §2.2).
type FieldSpec struct {
	// Key is the eternal field key (§2.2b): lowercase dotted, prefixed with
	// the entity type (e.g. "catalog.work.display_name"). Never renamed,
	// never reused; renaming a field = a new key + deprecating this one.
	Key      string
	Kind     FieldKind
	DiffHint string
	// Deprecated fields stay registered so historical revisions and diffs
	// keep rendering, but can no longer be proposed or amended.
	Deprecated bool
	// Policy overrides the spec's DefaultPolicy for this field (nil = use
	// the default). Site overlays trump both.
	Policy   *Policy
	Validate func(value any) error
	Apply    ApplyFunc
}

// EntityTypeSpec is one entity type's registration: field table, policies,
// and the load/apply closures carrying the family's own DB pool (dependency
// injection — the engine never imports a family package).
type EntityTypeSpec struct {
	Family string // e.g. "catalog"
	Type   string // e.g. "catalog.work" (must be Family + "." + name)
	// LoadSnapshot reads the entity's full registered-field state as a
	// field-key→value map. Returns ErrEntityNotFound (possibly wrapped) when
	// the entity row does not exist.
	LoadSnapshot func(ctx context.Context, entityID int64) (map[string]any, error)
	// Txn runs fn inside a transaction on the family's own DB pool; the
	// engine passes that transaction into each field's Apply.
	Txn           func(ctx context.Context, fn func(tx *gorm.DB) error) error
	Fields        []FieldSpec
	DefaultPolicy Policy
	// SiteOverlays: site → field key → policy replacement (doc 21 §2.5).
	// E0 ships the mechanism; per-site tuning starts with E1 tenants.
	SiteOverlays map[string]map[string]Policy

	fields map[string]*FieldSpec // built by Register
}

// Field returns the spec for a registered field key.
func (s *EntityTypeSpec) Field(key string) (*FieldSpec, bool) {
	f, ok := s.fields[key]
	return f, ok
}

// fieldForWrite resolves a key for a write path: unknown → UnknownFieldError,
// deprecated → ValidationError (still renderable in history, no new writes).
func (s *EntityTypeSpec) fieldForWrite(key string) (*FieldSpec, error) {
	f, ok := s.fields[key]
	if !ok {
		return nil, &UnknownFieldError{Key: key}
	}
	if f.Deprecated {
		return nil, &ValidationError{Key: key, Reason: "field is deprecated"}
	}
	return f, nil
}

// EffectivePolicy resolves a field's policy for a site: site overlay wins,
// then the field override, then the spec default.
func (s *EntityTypeSpec) EffectivePolicy(fieldKey, site string) Policy {
	if overlay, ok := s.SiteOverlays[site]; ok {
		if p, ok := overlay[fieldKey]; ok {
			return p
		}
	}
	if f, ok := s.fields[fieldKey]; ok && f.Policy != nil {
		return *f.Policy
	}
	return s.DefaultPolicy
}

// Registry holds the registered entity types. Populated once at the
// assembly point before serving; reads are lock-free thereafter.
type Registry struct {
	types map[string]*EntityTypeSpec
}

func NewRegistry() *Registry {
	return &Registry{types: make(map[string]*EntityTypeSpec)}
}

var keyPattern = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)

// Register validates and adds an entity type. It returns an error (rather
// than panicking) so assembly points fail their boot loudly.
func (r *Registry) Register(spec EntityTypeSpec) error {
	if spec.Family == "" || spec.Type == "" {
		return fmt.Errorf("editing: spec needs family and type")
	}
	if !strings.HasPrefix(spec.Type, spec.Family+".") {
		return fmt.Errorf("editing: type %q must be prefixed by family %q", spec.Type, spec.Family)
	}
	if spec.LoadSnapshot == nil || spec.Txn == nil {
		return fmt.Errorf("editing: type %q needs LoadSnapshot and Txn", spec.Type)
	}
	if len(spec.Fields) == 0 {
		return fmt.Errorf("editing: type %q registers no fields", spec.Type)
	}
	if _, dup := r.types[spec.Type]; dup {
		return fmt.Errorf("editing: type %q already registered", spec.Type)
	}
	if err := validatePolicy(spec.DefaultPolicy); err != nil {
		return fmt.Errorf("editing: type %q default policy: %w", spec.Type, err)
	}
	spec.fields = make(map[string]*FieldSpec, len(spec.Fields))
	for i := range spec.Fields {
		f := &spec.Fields[i]
		if !keyPattern.MatchString(f.Key) {
			return fmt.Errorf("editing: field key %q is not lowercase-dotted", f.Key)
		}
		if !strings.HasPrefix(f.Key, spec.Type+".") {
			return fmt.Errorf("editing: field key %q must be prefixed by type %q", f.Key, spec.Type)
		}
		if _, dup := spec.fields[f.Key]; dup {
			return fmt.Errorf("editing: duplicate field key %q", f.Key)
		}
		if f.Validate == nil || f.Apply == nil {
			return fmt.Errorf("editing: field %q needs Validate and Apply", f.Key)
		}
		if f.Policy != nil {
			if err := validatePolicy(*f.Policy); err != nil {
				return fmt.Errorf("editing: field %q policy: %w", f.Key, err)
			}
		}
		spec.fields[f.Key] = f
	}
	for site, overlay := range spec.SiteOverlays {
		for key, p := range overlay {
			if _, ok := spec.fields[key]; !ok {
				return fmt.Errorf("editing: site %q overlays unknown field %q", site, key)
			}
			if err := validatePolicy(p); err != nil {
				return fmt.Errorf("editing: site %q overlay for %q: %w", site, key, err)
			}
		}
	}
	r.types[spec.Type] = &spec
	return nil
}

// Type returns a registered entity type spec.
func (r *Registry) Type(entityType string) (*EntityTypeSpec, bool) {
	s, ok := r.types[entityType]
	return s, ok
}

func validatePolicy(p Policy) error {
	switch {
	case p.Propose == ProposeOpen, p.Propose == ProposeTrusted, p.Propose == ProposeLocked:
	case strings.HasPrefix(p.Propose, permPrefix) && len(p.Propose) > len(permPrefix):
	default:
		return fmt.Errorf("bad propose rule %q", p.Propose)
	}
	if !strings.HasPrefix(p.Review, permPrefix) || len(p.Review) == len(permPrefix) {
		return fmt.Errorf("bad review rule %q (must be perm:<key>)", p.Review)
	}
	switch p.Automerge {
	case AutomergeNever, AutomergeTrusted, AutomergeAlways:
	default:
		return fmt.Errorf("bad automerge rule %q", p.Automerge)
	}
	return nil
}
