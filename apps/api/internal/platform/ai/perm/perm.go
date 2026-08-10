package perm

import "api/internal/platform/authz"

const UsageView authz.Permission = "ai.usage_view"

var adminPerms = []authz.Permission{UsageView}

var Bundles = authz.Bundles{
	"admin": adminPerms,
	"ren":   adminPerms,
}

var Resolver = authz.NewHolder(Bundles)
