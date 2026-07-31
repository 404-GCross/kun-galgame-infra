// Package perm is the catalog domain's permission vocabulary: the ren-only
// internal admin/review surface (cross-media identity registry — merge/
// unmerge, reconcile queues, quality panels) plus the editing-engine field
// policy keys (E0). Bundles is the golden table; perm_test.go pins every row.
package perm

import "api/internal/platform/authz"

// Review: reach the catalog internal review/admin surface (S2S read proxy,
// reconcile queues, entity browser). ren-only.
const Review authz.Permission = "catalog.review"

// ClaimReview: decide a product site's submission — approve / decline / ban /
// unban a claim (wave 157). Deliberately NOT the same key as Review: judging
// submissions is ordinary content moderation, staffed by moderators on every
// surface this platform has ever shipped, while Review is curation of the
// identity registry itself (merge/unmerge, reconcile queues) and stays
// ren-only. Keying the claim actions to Review would have narrowed a live
// product right from "moderator and up" to one person the moment the wiki's
// submission queue moved here — a regression dressed as a migration.
const ClaimReview authz.Permission = "catalog.claim.review"

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

// Vocabulary-layer editing keys (wave 154, 03 定案 §4). ONE pair covers all
// four narrow families — catalog.{label,tag,engine,series} — because they are
// one authority: the registry's shared vocabulary, whose entries every
// consumer's pages hang off. Splitting the key per family would suggest a
// distinction ("may edit engines but not series") that nothing on the platform
// makes. Creating, deleting and merging vocabulary entries is NOT covered here:
// that is curation of the registry itself and stays behind catalog.review.
const (
	EditTaxonomy       authz.Permission = "edit.catalog.taxonomy"
	EditTaxonomyReview authz.Permission = "edit.catalog.taxonomy.review"
)

// moderatorPerms is content moderation on the registry: today exactly the
// claim review queue — the successor of the wiki submission queue moderators
// have always staffed.
var moderatorPerms = []authz.Permission{ClaimReview}

// adminPerms adds the editing keys on top: site curation is an admin duty,
// unlike merge/unmerge which stays ren-only.
var adminPerms = append(append([]authz.Permission{}, moderatorPerms...),
	EditWork, EditWorkReview, EditTaxonomy, EditTaxonomyReview)

// renPerms adds the identity-registry surface itself.
var renPerms = append(append([]authz.Permission{}, adminPerms...), Review)

// Bundles is the catalog domain's role→permission table. The three bundles are
// composed upward rather than hand-listed, so the contract's management-axis
// containment (moderator ⊆ admin ⊆ ren) holds by construction — the property
// perm_test.go asserts, here made true by the shape of the code.
var Bundles = authz.Bundles{
	"moderator": moderatorPerms,
	"admin":     adminPerms,
	"ren":       renPerms,
}

// Resolver is the package-level singleton the catalog enforcement points check.
var Resolver = authz.NewResolver(Bundles)
