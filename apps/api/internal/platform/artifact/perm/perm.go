package perm

import "api/internal/platform/authz"

const FilesManage authz.Permission = "artifact.files.manage"

var Bundles = authz.Bundles{
	"ren": {FilesManage},
}

var Resolver = authz.NewHolder(Bundles)
