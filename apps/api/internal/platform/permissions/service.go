package permissions

import (
	"context"

	"api/internal/platform/authz"
)

// Grant sources, as the matrix reports them per cell.
const (
	// SourceNone: the role does not hold the key.
	SourceNone = "none"
	// SourceCode: granted by the compiled-in bundle — the floor, not revocable.
	SourceCode = "code"
	// SourceOverlay: granted by a role_permission_overrides row — revocable.
	SourceOverlay = "overlay"
)

// Cell is one (permission, role) square of the matrix.
type Cell struct {
	Granted bool   `json:"granted"`
	Source  string `json:"source"`
	// Editable is whether THIS caller may toggle this cell, decided server-side
	// by the same validator the write path runs. The frontend renders it; it
	// never recomputes authorization.
	Editable bool `json:"editable"`
	// Reason explains a non-editable cell (empty when editable).
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
		st.Set(r.Role, authz.Permission(r.Permission))
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

// cell decides one square: its current grant, where the grant comes from, and
// whether this caller may flip it. "May flip it" is literally "would the write
// validate", so the button the UI offers is exactly the write it can make.
func (s *Service) cell(caller Caller, st OverlayState, role string, p authz.Permission) Cell {
	granted := s.reg.Effective(role, p)
	source := SourceNone
	switch {
	case st.Has(role, p):
		source = SourceOverlay
	case granted:
		source = SourceCode
	}

	c := Cell{Granted: granted, Source: source}
	err := s.val.Validate(caller, st, Action{Grant: !granted, Role: role, Permission: p})
	if err == nil {
		c.Editable = true
	} else {
		c.Reason = err.Error()
	}
	return c
}

// Apply validates and performs one overlay write, then makes it take effect:
// this process refreshes synchronously (so the caller's very next request sees
// the new table) and peers are nudged to do the same.
func (s *Service) Apply(ctx context.Context, caller Caller, act Action) error {
	st, err := s.state(ctx)
	if err != nil {
		return err
	}
	if err := s.val.Validate(caller, st, act); err != nil {
		return err
	}

	if act.Grant {
		err = s.store.Grant(ctx, act.Role, string(act.Permission), caller.UserID)
	} else {
		err = s.store.Revoke(ctx, act.Role, string(act.Permission), caller.UserID)
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
