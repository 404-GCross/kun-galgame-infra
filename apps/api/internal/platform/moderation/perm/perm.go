// Package perm is the moderation domain's permission vocabulary. The
// image-moderation review queue is content-moderation staff tooling. Bundles is
// the golden table; perm_test.go pins every row.
package perm

import "api/internal/platform/authz"

// QueueAccess: reach the image-moderation review queue (list jobs, manual
// review, list policies).
const QueueAccess authz.Permission = "moderation.queue_access"

// moderatorPerms is the content-moderation staff bundle; admin and ren inherit
// it unchanged (the management axis has no moderation-specific escalation).
var moderatorPerms = []authz.Permission{QueueAccess}

// Bundles is the moderation domain's role→permission table.
var Bundles = authz.Bundles{
	"moderator": moderatorPerms,
	"admin":     moderatorPerms,
	"ren":       moderatorPerms,
}

// Resolver is the package-level singleton the moderation enforcement points check.
var Resolver = authz.NewResolver(Bundles)
