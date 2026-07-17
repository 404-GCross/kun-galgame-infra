// Package perm is the catalog domain's permission vocabulary: the ren-only
// internal admin/review surface (cross-media identity registry — merge/
// unmerge, reconcile queues, quality panels) plus the editing-engine field
// policy keys (E0). Bundles is the golden table; perm_test.go pins every row.
package perm

import "api/internal/platform/authz"

// Review: reach the catalog internal review/admin surface (S2S read proxy,
// reconcile queues, entity browser). ren-only.
const Review authz.Permission = "catalog.review"

// Editing-engine field policy keys (doc 21 §2.5, added per docs/auth/04
// §2.5). The engine's catalog.work default policy references these:
// EditWork gates proposing, EditWorkReview gates amend/merge/decline/revert.
// E1 tenant users get their propose/direct rights via trust tiers and site
// overlays, NOT via these global roles — the global grants stay curation-
// staff only (admin + ren; management-axis containment holds).
const (
	EditWork       authz.Permission = "edit.catalog.work"
	EditWorkReview authz.Permission = "edit.catalog.work.review"
)

// Bundles is the catalog domain's role→permission table. The identity-
// registry surface stays ren-only; the editing keys extend to admin (site
// curation is an admin duty, unlike merge/unmerge which stays ren-only).
var Bundles = authz.Bundles{
	"admin": {EditWork, EditWorkReview},
	"ren":   {Review, EditWork, EditWorkReview},
}

// Resolver is the package-level singleton the catalog enforcement points check.
var Resolver = authz.NewResolver(Bundles)
