// Package perm is the catalog domain's permission vocabulary. The catalog
// admin/review surface is entirely ren-only internal staff tooling (cross-media
// identity registry — merge/unmerge, reconcile queues, quality panels), so the
// domain currently needs a single capability. Bundles is the golden table;
// perm_test.go pins every row.
package perm

import "api/internal/platform/authz"

// Review: reach the catalog internal review/admin surface (S2S read proxy,
// reconcile queues, entity browser). ren-only.
const Review authz.Permission = "catalog.review"

// Bundles is the catalog domain's role→permission table. Only `ren` grants
// anything here; every other role — including admin/moderator — is not part of
// this internal staff surface.
var Bundles = authz.Bundles{
	"ren": {Review},
}

// Resolver is the package-level singleton the catalog enforcement points check.
var Resolver = authz.NewResolver(Bundles)
