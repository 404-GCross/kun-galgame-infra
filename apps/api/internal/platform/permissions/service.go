package permissions

import (
	"context"

	"api/internal/platform/authz"
)

const (
	SourceNone  = "none"
	SourceCode  = "code"
	SourceGrant = "grant"
	SourceDeny  = "deny"
)

type Cell struct {
	Granted    bool   `json:"granted"`
	Source     string `json:"source"`
	Editable   bool   `json:"editable"`
	CanDeny    bool   `json:"can_deny"`
	CanRestore bool   `json:"can_restore"`
	Reason     string `json:"reason,omitempty"`
}

type KeyRow struct {
	Key          string          `json:"key"`
	DescEN       string          `json:"desc_en"`
	DescZH       string          `json:"desc_zh"`
	NonDelegable bool            `json:"non_delegable"`
	Grants       map[string]Cell `json:"grants"`
}

type DomainView struct {
	Name    string   `json:"name"`
	TitleZH string   `json:"title_zh"`
	Keys    []KeyRow `json:"keys"`
}

type Matrix struct {
	Roles              []string     `json:"roles"`
	EditableRoles      []string     `json:"editable_roles"`
	ManagesPermissions bool         `json:"manages_permissions"`
	Domains            []DomainView `json:"domains"`
}

type Service struct {
	reg   *Registry
	store *Store
	val   *Validator
	dist  *Distributor
}

func NewService(reg *Registry, store *Store, dist *Distributor) *Service {
	return &Service{reg: reg, store: store, val: NewValidator(reg), dist: dist}
}

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

type WriteRequest struct {
	Add        bool
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
		act.Op = OpRevokeGrant
	}
	return act
}

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

func (s *Service) Audit(ctx context.Context, limit int) ([]AuditEntry, error) {
	return s.store.RecentAudit(ctx, limit)
}
