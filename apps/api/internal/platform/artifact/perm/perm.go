// Package perm is the artifact domain's permission vocabulary. The admin
// artifact operations (browse/delete/reclaim stored files) are ren-only file
// operations, layered under the oauth console's admin gate. Bundles is the
// golden table; perm_test.go pins every row.
package perm

import "api/internal/platform/authz"

// FilesManage: browse, delete, and reclaim stored artifact files. ren-only —
// the extra restriction on top of the console's admin-gated prefix.
const FilesManage authz.Permission = "artifact.files.manage"

// Bundles is the artifact domain's role→permission table.
var Bundles = authz.Bundles{
	"ren": {FilesManage},
}

// Resolver is the package-level singleton the artifact enforcement points check.
var Resolver = authz.NewResolver(Bundles)
