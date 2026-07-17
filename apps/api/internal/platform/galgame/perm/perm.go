// Package perm is the galgame domain's permission vocabulary: the operation
// capabilities the galgame service enforces, plus the role→permission bundles
// that decide who holds them. It is the single place a role string is mapped
// to galgame capabilities — every enforcement point in the domain checks a
// Permission via Resolver.Can, never a role string.
//
// This package lives UNDER internal/platform/galgame (product side), so it may
// import the platform authz engine; the engine never imports back, keeping the
// dependency direction intact. The bundles below are the golden authorization
// table for the domain — perm_test.go pins every row.
package perm

import "api/internal/platform/authz"

// Permission constants. Naming: <domain>.<verb> or <domain>.<object>.<verb>,
// all lower-case, snake_case within a segment.
const (
	// PublishDirect: publish a galgame straight to status=0, skipping the
	// review queue and the daily submission quota (the trusted-publisher path).
	PublishDirect authz.Permission = "galgame.publish_direct"
	// Review: staff the review queue — preview others' pending/declined
	// submissions.
	Review authz.Permission = "galgame.review"
	// EditAny: direct-edit any galgame regardless of owner or status (the
	// content-moderation edit power, distinct from the owner-or-admin override).
	EditAny authz.Permission = "galgame.edit_any"
	// Create: the admin direct-publish route (POST /galgame) that bypasses the
	// user submission queue.
	Create authz.Permission = "galgame.create"
	// AdminAccess: reach the /admin/galgame/* management surface.
	AdminAccess authz.Permission = "galgame.admin_access"
	// TaxonomyEditAny: create/update/delete/revert taxonomy entities (tag,
	// engine, official, series).
	TaxonomyEditAny authz.Permission = "galgame.taxonomy.edit_any"
	// TaxonomyReview: revert taxonomy revisions.
	TaxonomyReview authz.Permission = "galgame.taxonomy.review"
	// SearchAllStates: query the public search across every status (banned,
	// pending, declined) instead of the published-only public clamp. Renamed
	// from the step-01 draft name galgame.search.admin to describe the actual
	// capability rather than a role.
	SearchAllStates authz.Permission = "galgame.search.all_states"
	// OwnerOverride: act on a resource you don't own — the admin branch of the
	// owner-or-admin gates (link/alias/contributor mutation, revision
	// revert/merge/decline). Narrower than EditAny: admin/ren only, NOT
	// moderator, matching the pre-migration "owner or admin" checks.
	OwnerOverride authz.Permission = "galgame.owner_override"

	// Editing-engine field-policy keys (E2a, docs/auth/04 §2.5 — new keys, no
	// new roles). These are the galgame.game entity's review/propose gates in
	// the unified editing engine; the strangler adapter also grants them
	// situationally (e.g. the entity creator merging a PR on their own galgame,
	// a submitter fixing vndb_id on their own draft) — the bundles below are
	// only the ROLE-carried grants.

	// EditGameReview: adjudicate galgame.game proposals (merge / decline /
	// amend / revert) — the engine-era successor of the owner-or-admin
	// override on PR merge/decline/revert, so it follows the OwnerOverride
	// axis (admin/ren, not moderator).
	EditGameReview authz.Permission = "edit.galgame.game.review"
	// EditGameStatus: direct-edit the galgame.game.status management field
	// (approve / decline / ban / unban transitions land as engine direct
	// edits). Follows the AdminAccess axis (moderator+).
	EditGameStatus authz.Permission = "edit.galgame.game.status"
	// EditGameVNDBID: change galgame.game.vndb_id. The squatting lesson makes
	// this staff-only on published entries (EditAny axis, moderator+); a
	// submitter fixing their own unpublished draft gets it granted by the
	// adapter, not by role.
	EditGameVNDBID authz.Permission = "edit.galgame.game.vndb_id"
)

// moderatorPerms is everything content-moderation staff can do in the galgame
// domain. admin = moderator + owner_override; ren = admin (the contract's
// ren ⊇ admin, with no galgame-specific ren-only power). Composing the higher
// bundles from the lower ones makes the management-axis containment
// (moderator ⊆ admin ⊆ ren) hold by construction, not by hand-copied lists.
var moderatorPerms = []authz.Permission{
	PublishDirect,
	Review,
	EditAny,
	Create,
	AdminAccess,
	TaxonomyEditAny,
	TaxonomyReview,
	SearchAllStates,
	EditGameStatus,
	EditGameVNDBID,
}

// adminPerms adds the owner-or-admin override — and its editing-engine
// successor, the galgame.game review key — on top of the moderator bundle.
var adminPerms = append(append([]authz.Permission{}, moderatorPerms...), OwnerOverride, EditGameReview)

// Bundles is the galgame domain's role→permission table. Only the four
// grantable roles appear; `user` is implicit (login, handled by JWTAuth) and
// grants nothing here, and the retired top-tier alias is deliberately absent
// (the IdP never issues it — it is now just an unknown role). creator is
// orthogonal to the management axis — it grants only the trusted-publisher
// capability.
var Bundles = authz.Bundles{
	"creator":   {PublishDirect},
	"moderator": moderatorPerms,
	"admin":     adminPerms,
	"ren":       adminPerms,
}

// Resolver is the package-level singleton the galgame enforcement points check.
var Resolver = authz.NewResolver(Bundles)
