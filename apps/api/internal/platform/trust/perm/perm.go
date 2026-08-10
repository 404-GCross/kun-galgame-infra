package perm

import "api/internal/platform/authz"

const QueueAccess authz.Permission = "trust.queue_access"

const TermManage authz.Permission = "trust.term_manage"

var moderatorPerms = []authz.Permission{QueueAccess}

var adminPerms = append(append([]authz.Permission{}, moderatorPerms...), TermManage)

var Bundles = authz.Bundles{
	"moderator": moderatorPerms,
	"admin":     adminPerms,
	"ren":       adminPerms,
}

var Resolver = authz.NewHolder(Bundles)
