// Package perm is the AI-gateway domain's permission vocabulary. The AI admin
// surface is the usage/cost/budget dashboard (doc 20 §5) — an OPERATIONS face,
// not a content-moderation one — so the domain needs a single capability that,
// unlike the trust queue, is deliberately NOT granted to moderator: usage cost
// is a management concern. Bundles is the golden table; perm_test.go pins every
// row. Mirrors trust/catalog perm-package shape (章程 04 §2.2).
package perm

import "api/internal/platform/authz"

// UsageView: reach the AI-gateway usage dashboard (site×route×channel usage,
// cost, error rate, and the per-route budget fuse config).
const UsageView authz.Permission = "ai.usage_view"

// adminPerms is the management bundle for the usage dashboard. moderator is
// intentionally absent (usage cost is ops, not content moderation); admin and
// ren inherit it (admin ⊆ ren on the management axis).
var adminPerms = []authz.Permission{UsageView}

// Bundles is the AI domain's role→permission table.
var Bundles = authz.Bundles{
	"admin": adminPerms,
	"ren":   adminPerms,
}

// Resolver is the package-level singleton the AI admin enforcement point checks.
var Resolver = authz.NewResolver(Bundles)
