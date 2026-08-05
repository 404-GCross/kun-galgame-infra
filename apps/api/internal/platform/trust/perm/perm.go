// Package perm is the Trust & Safety domain's permission vocabulary. The trust
// admin surface is the unified review inbox — moderation staff tooling — so the
// domain needs a single capability. Bundles is the golden table; perm_test.go
// pins every row. (This package supersedes the retired moderation/perm that
// died with the moderation skeleton service — doc 18 P0, 章程 ruling 13.)
package perm

import "api/internal/platform/authz"

// QueueAccess: reach the T&S review inbox (list/claim/decide items, manage the
// subject-kind + reason registries, replay dead-letter callbacks).
const QueueAccess authz.Permission = "trust.queue_access"

// TermManage: create/deprecate Tier0 word-list terms (step 05). Deliberately
// NARROWER than queue_access — a term is a site-domain ban power, more sensitive
// than reviewing the inbox — so moderator is EXCLUDED; only admin/ren hold it
// (mirroring the ai.usage_view bundling decision, docs/auth/04 §2.5).
const TermManage authz.Permission = "trust.term_manage"

// moderatorPerms is the content-moderation staff bundle; admin and ren inherit
// it unchanged (the management axis has no trust-specific escalation).
var moderatorPerms = []authz.Permission{QueueAccess}

// adminPerms is the management bundle: everything moderator has, plus the more
// sensitive term-management power. admin and ren inherit it (admin ⊆ ren).
var adminPerms = append(append([]authz.Permission{}, moderatorPerms...), TermManage)

// Bundles is the trust domain's role→permission table.
var Bundles = authz.Bundles{
	"moderator": moderatorPerms,
	"admin":     adminPerms,
	"ren":       adminPerms,
}

// Resolver is the package-level Holder the trust enforcement points check.
// It starts at the code bundles and is swapped whole when the permission
// overlay refreshes (docs/auth/04 §7).
var Resolver = authz.NewHolder(Bundles)
