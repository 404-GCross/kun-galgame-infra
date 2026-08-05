// Package perm is the developer-platform (NextMoe open API) permission
// vocabulary. A single capability gates the admin management surface that
// enables applications for the open API, sets their tier/quota, and mints /
// rotates / revokes API keys. Unlike the catalog/artifact staff surfaces
// (ren-only), the developer-platform admin console follows the oauth console's
// gate — admin AND ren both manage it (the first trusted keys are minted by
// ordinary admins). Bundles is the golden table; perm_test.go pins every row.
package perm

import "api/internal/platform/authz"

// Manage: reach the developer-platform admin surface — enable/disable apps for
// the open API, set tier / nsfw / rate / quota overrides, and mint / rotate /
// revoke API keys. Held by admin and ren.
const Manage authz.Permission = "devapi.manage"

// adminPerms is what an ordinary admin holds on this surface. renPerms composes
// it so admin ⊆ ren by construction.
var adminPerms = []authz.Permission{Manage}

var renPerms = append([]authz.Permission{}, adminPerms...)

// Bundles is the developer-platform's role→permission table. moderator/creator
// have no authority here; `user` is implicit and grants nothing.
var Bundles = authz.Bundles{
	"admin": adminPerms,
	"ren":   renPerms,
}

// Resolver is the package-level Holder the enforcement points check.
// It starts at the code bundles and is swapped whole when the permission
// overlay refreshes (docs/auth/04 §7).
var Resolver = authz.NewHolder(Bundles)
