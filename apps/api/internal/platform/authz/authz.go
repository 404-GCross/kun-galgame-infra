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

// Permission is one operation capability, e.g. "galgame.publish_direct".
type Permission string

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
