package perm

import "api/internal/platform/authz"

const Manage authz.Permission = "devapi.manage"

var adminPerms = []authz.Permission{Manage}

var renPerms = append([]authz.Permission{}, adminPerms...)

var Bundles = authz.Bundles{
	"admin": adminPerms,
	"ren":   renPerms,
}

var Resolver = authz.NewHolder(Bundles)
