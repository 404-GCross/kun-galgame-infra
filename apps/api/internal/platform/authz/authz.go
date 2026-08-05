// Package authz is the platform's permission-first authorization engine. It
// answers a single question — "does this caller, given its roles, hold a
// permission?" — and knows nothing about any product's vocabulary: a role is
// just an opaque string (the exact JWT `roles` claim value) and a permission
// is just an opaque string constant. Each domain supplies its own bundles
// (role → permissions) and builds a Resolver from them; this package never
// imports a product domain, so it stays on the platform side of the
// dependency-direction invariant.
//
// Roles are a bundle of permissions, encoded as data. Any capability hierarchy
// (e.g. the contract's ren ⊇ admin ⊇ moderator) is expressed by making the
// higher role's bundle a superset of the lower one — not by wildcard or level
// logic here. That keeps the engine trivial and pushes every product decision
// into the domain's bundle table, where a golden test can pin it.
package authz

import "sync/atomic"

// Permission is one operation capability, e.g. "galgame.publish_direct".
type Permission string

// NonDelegable is the set of permissions that may NEVER be granted through the
// runtime overlay (the role_permission_overrides table), by anyone — not even
// the caller who holds oauth.permissions.manage. They are the keys whose own
// grant would let a holder rewrite the permission system itself or escape the
// console's ownership scoping, so the only way to move them is a code change +
// deploy, which leaves a reviewable diff. A domain declares its own set; a
// domain that declares none has no such key.
type NonDelegable map[Permission]bool

// Has reports whether p is non-delegable. Safe on a nil set.
func (n NonDelegable) Has(p Permission) bool { return n[p] }

// Checker is the read side of the engine — the only thing an enforcement point
// needs. Both Resolver (fixed) and Holder (hot-swappable) satisfy it, so route
// gates and in-handler checks are written against the interface and keep
// working when a domain's grants become swappable at runtime.
type Checker interface {
	Can(roles []string, p Permission) bool
}

// Bundles maps a role name (the exact `roles` claim string) to the permissions
// that role grants on one service surface. A role absent from the map grants
// nothing.
type Bundles map[string][]Permission

// Resolver answers permission checks against a fixed set of role→permission
// bundles. Build it once at startup with NewResolver and share it: it is
// immutable after construction and safe for concurrent reads.
type Resolver struct {
	grants map[string]map[Permission]struct{}
}

// NewResolver builds a Resolver from bundles. The bundles are copied into an
// internal set representation, so mutating b afterwards has no effect on the
// resolver.
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

// Can reports whether ANY of the caller's roles grants permission p. It is
// fail-closed: nil/empty roles, a role not present in the bundles, and a
// permission granted by no role all yield false. Login itself is not a
// permission — that stays with the JWT middleware; Can only decides elevation.
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

// Holder is a Checker whose Resolver can be replaced at runtime. The Resolver
// itself stays immutable — a refresh builds a WHOLE new one and swaps the
// pointer — so readers never observe a half-applied grant table and no lock is
// taken on the hot path.
//
// It exists because a domain's grants are no longer purely compile-time: the
// permission console's overlay (role_permission_overrides) is merged on top of
// the code bundles and can change while the process runs. Enforcement points
// hold the Holder, never the Resolver it currently points at, so a swap
// actually takes effect at every gate registered at startup.
type Holder struct {
	current atomic.Pointer[Resolver]
}

// NewHolder builds a Holder around the code bundles. This is the floor: until
// an overlay is loaded (or when the overlay source is unreachable), the process
// enforces exactly the compiled-in table.
func NewHolder(b Bundles) *Holder {
	h := &Holder{}
	h.Swap(NewResolver(b))
	return h
}

// Swap installs a freshly built Resolver. Callers build the new Resolver from
// code bundles ∪ overlay and hand it over whole.
func (h *Holder) Swap(r *Resolver) { h.current.Store(r) }

// Resolver returns the Resolver currently in force (useful for read-only
// introspection such as the permission matrix export).
func (h *Holder) Resolver() *Resolver { return h.current.Load() }

// Can delegates to the Resolver currently in force.
func (h *Holder) Can(roles []string, p Permission) bool {
	return h.current.Load().Can(roles, p)
}
