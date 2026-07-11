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

// moderatorPerms is the content-moderation staff bundle; admin and ren inherit
// it unchanged (the management axis has no trust-specific escalation).
var moderatorPerms = []authz.Permission{QueueAccess}

// Bundles is the trust domain's role→permission table.
var Bundles = authz.Bundles{
	"moderator": moderatorPerms,
	"admin":     moderatorPerms,
	"ren":       moderatorPerms,
}

// Resolver is the package-level singleton the trust enforcement points check.
var Resolver = authz.NewResolver(Bundles)
