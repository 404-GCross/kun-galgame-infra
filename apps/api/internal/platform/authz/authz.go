package authz

import "sync/atomic"

type Permission string

type NonDelegable map[Permission]bool

func (n NonDelegable) Has(p Permission) bool { return n[p] }

type Checker interface {
	Can(roles []string, p Permission) bool
}

type Bundles map[string][]Permission

type Resolver struct {
	grants map[string]map[Permission]struct{}
}

func NewResolver(b Bundles) *Resolver {
	grants := make(map[string]map[Permission]struct{}, len(b))
	for role, perms := range b {
		set := make(map[Permission]struct{}, len(perms))
		for _, p := range perms {
			set[p] = struct{}{}
		}
		grants[role] = set
	}
	return &Resolver{grants: grants}
}

func (r *Resolver) Can(roles []string, p Permission) bool {
	for _, role := range roles {
		if set, ok := r.grants[role]; ok {
			if _, ok := set[p]; ok {
				return true
			}
		}
	}
	return false
}

type Holder struct {
	current atomic.Pointer[Resolver]
}

func NewHolder(b Bundles) *Holder {
	h := &Holder{}
	h.Swap(NewResolver(b))
	return h
}

func (h *Holder) Swap(r *Resolver) { h.current.Store(r) }

func (h *Holder) Resolver() *Resolver { return h.current.Load() }

func (h *Holder) Can(roles []string, p Permission) bool {
	return h.current.Load().Can(roles, p)
}
