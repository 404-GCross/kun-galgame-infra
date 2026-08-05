package permissions

import (
	"context"

	"api/internal/platform/authz"
)

// Cell sources, as the matrix reports them per square. They are exactly the
// four states a cell can be in, and each one admits exactly ONE operation —
// which is why the frontend never has to choose between two buttons.
const (
	// SourceNone: the role does not hold the key. → OpGrant
	SourceNone = "none"
	// SourceCode: held via the compiled-in bundle, with no overlay row. → OpDeny
	SourceCode = "code"
	// SourceGrant: held via an EffectGrant row. → OpRevokeGrant
	SourceGrant = "grant"
	// SourceDeny: withheld by an EffectDeny row despite the code bundle.
	// → OpRevokeDeny
	SourceDeny = "deny"
)

// Cell is one (permission, role) square of the matrix.
type Cell struct {
	Granted bool   `json:"granted"`
	Source  string `json:"source"`
	// Editable is whether THIS caller may add or remove a GRANT here — the
	// original toggle. Decided server-side by the same validator the write path
	// runs; the frontend renders the verdict and never recomputes it.
	Editable bool `json:"editable"`
	// CanDeny is whether this caller may write a deny row here: a code-floor
	// grant on an editable role that no invariant pins in place.
	CanDeny bool `json:"can_deny"`
	// CanRestore is whether this caller may delete the deny row here, returning
	// the role to its code floor.
	CanRestore bool `json:"can_restore"`
	// Reason explains a cell that offers NO operation at all (empty otherwise).
	Reason string `json:"reason,omitempty"`
}

// KeyRow is one permission with its five role cells.
type KeyRow struct {
	Key          string          `json:"key"`
	DescEN       string          `json:"desc_en"`
	DescZH       string          `json:"desc_zh"`
	NonDelegable bool            `json:"non_delegable"`
	Grants       map[string]Cell `json:"grants"`
}

// DomainView is one domain's block of the matrix.
type DomainView struct {
	Name    string   `json:"name"`
	TitleZH string   `json:"title_zh"`
	Keys    []KeyRow `json:"keys"`
}

// Matrix is the whole console export.
type Matrix struct {
	Roles []string `json:"roles"`
	// EditableRoles are the rows the overlay may ever touch, so the frontend
	// can grey the immutable columns without knowing why.
	EditableRoles []string `json:"editable_roles"`
	// ManagesPermissions is whether the caller holds oauth.permissions.manage.
	ManagesPermissions bool         `json:"manages_permissions"`
	Domains            []DomainView `json:"domains"`
}

// Service is the console's use-case layer: it reads the overlay once, builds
// the matrix (including this caller's editable cells), and performs validated
// writes followed by an immediate local refresh + peer announcement.
type Service struct {
	reg   *Registry
	store *Store
	val   *Validator
	dist  *Distributor
}

// NewService wires the console service.
func NewService(reg *Registry, store *Store, dist *Distributor) *Service {
	return &Service{reg: reg, store: store, val: NewValidator(reg), dist: dist}
}

// state reads the overlay rows into the validator's view of them.
func (s *Service) state(ctx context.Context) (OverlayState, error) {
	rows, err := s.store.Overrides(ctx)
	if err != nil {
		return nil, err
	}
	st := make(OverlayState, len(rows))
	for _, r := range rows {
		st.Set(r.Role, authz.Permission(r.Permission), r.Effect)
	}
	return st, nil
}

// Matrix builds the full export for caller.
func (s *Service) Matrix(ctx context.Context, caller Caller) (*Matrix, error) {
	st, err := s.state(ctx)
	if err != nil {
		return nil, err
	}

	out := &Matrix{
		Roles:              MatrixRoles,
		EditableRoles:      EditableRoles,
		ManagesPermissions: ManagesPermissions(caller.Roles),
	}
	for _, dom := range s.reg.Domains() {
		view := DomainView{Name: dom.Name, TitleZH: dom.TitleZH, Keys: make([]KeyRow, 0, len(dom.Keys))}
		for _, k := range dom.Keys {
			row := KeyRow{
				Key:          string(k.Permission),
				DescEN:       k.DescEN,
				DescZH:       k.DescZH,
				NonDelegable: dom.NonDelegable.Has(k.Permission),
				Grants:       make(map[string]Cell, len(MatrixRoles)),
			}
			for _, role := range MatrixRoles {
				row.Grants[role] = s.cell(caller, st, role, k.Permission)
			}
			view.Keys = append(view.Keys, row)
		}
		out.Domains = append(out.Domains, view)
	}
	return out, nil
}

// sourceOf classifies one square. The overlay row, when there is one, decides:
// its effect IS the state, and the code bundle only decides the roleless cells.
func sourceOf(granted bool, effect string) string {
	switch {
	case effect == EffectGrant:
		return SourceGrant
	case effect == EffectDeny:
		return SourceDeny
	case granted:
		return SourceCode
	default:
		return SourceNone
	}
}

// opFor is the ONE operation a cell in this state admits. Every other operation
// on that cell is refused by a state precondition, so offering it would be
// offering a button that cannot work.
func opFor(source string) Op {
	switch source {
	case SourceGrant:
		return OpRevokeGrant
	case SourceDeny:
		return OpRevokeDeny
	case SourceCode:
		return OpDeny
	default:
		return OpGrant
	}
}

// cell decides one square: whether the key is held, where that comes from, and
// which operation — if any — this caller may perform on it. "May perform" is
// literally "would the write validate", so the button the UI offers is exactly
// the write the API would accept.
func (s *Service) cell(caller Caller, st OverlayState, role string, p authz.Permission) Cell {
	granted := s.reg.Effective(role, p)
	source := sourceOf(granted, st.Effect(role, p))
	op := opFor(source)

	c := Cell{Granted: granted, Source: source}
	if err := s.val.Validate(caller, st, Action{Op: op, Role: role, Permission: p}); err != nil {
		c.Reason = err.Error()
		return c
	}
	switch op {
	case OpGrant, OpRevokeGrant:
		c.Editable = true
	case OpDeny:
		c.CanDeny = true
	case OpRevokeDeny:
		c.CanRestore = true
	}
	return c
}

// WriteRequest is what a console endpoint asks for, before the state is known.
// A removal does not say WHICH row it removes: the caller sees a cell, not a
// row, and which of the two deletions it meant follows from what is there. The
// service resolves that against the same state it validates against, so the
// operation cannot be resolved one way and validated another.
type WriteRequest struct {
	// Add distinguishes an INSERT from a DELETE.
	Add bool
	// Effect is EffectGrant or EffectDeny; read only when Add is true.
	Effect     string
	Role       string
	Permission authz.Permission
}

func (s *Service) resolve(st OverlayState, req WriteRequest) Action {
	act := Action{Role: req.Role, Permission: req.Permission}
	switch {
	case req.Add && req.Effect == EffectDeny:
		act.Op = OpDeny
	case req.Add:
		act.Op = OpGrant
	case st.HasDeny(req.Role, req.Permission):
		act.Op = OpRevokeDeny
	default:
		// Including "no row at all": OpRevokeGrant is the operation whose
		// precondition explains what to do instead.
		act.Op = OpRevokeGrant
	}
	return act
}

// Apply validates and performs one overlay write, then makes it take effect:
// this process refreshes synchronously (so the caller's very next request sees
// the new table) and peers are nudged to do the same.
func (s *Service) Apply(ctx context.Context, caller Caller, req WriteRequest) error {
	st, err := s.state(ctx)
	if err != nil {
		return err
	}
	act := s.resolve(st, req)
	if err := s.val.Validate(caller, st, act); err != nil {
		return err
	}

	if act.writesRow() {
		err = s.store.Add(ctx, act.Role, string(act.Permission), act.effect(), caller.UserID)
	} else {
		err = s.store.Remove(ctx, act.Role, string(act.Permission), caller.UserID)
	}
	if err != nil {
		return err
	}

	if err := s.dist.Refresh(ctx); err != nil {
		return err
	}
	s.dist.Announce(ctx)
	return nil
}

// Audit returns the newest audit rows.
func (s *Service) Audit(ctx context.Context, limit int) ([]AuditEntry, error) {
	return s.store.RecentAudit(ctx, limit)
}
