// Package perm is the IdP admin console's permission vocabulary. The console
// spans the auth and site packages (user/role management + site/OAuth-client
// management) but registers under one service (cmd/oauth), so a single leaf
// package carries the whole `oauth.*` vocabulary. It imports only the platform
// authz engine (no auth/site code), so auth handlers may import it without
// disturbing the existing auth→site dependency direction.
//
// Bundles is the golden table; perm_test.go pins every row.
package perm

import "api/internal/platform/authz"

// Permission constants. Naming: oauth.<verb> or oauth.<object>.<verb>.
const (
	// AdminAccess: reach the admin console at all (the /admin, /sites,
	// /oauth/clients, /admin/artifact route groups).
	AdminAccess authz.Permission = "oauth.admin_access"
	// UsersPIIView: see user PII (email in the list, email+IP in the detail).
	UsersPIIView authz.Permission = "oauth.users.pii_view"
	// RolesGrantBasic: grant/revoke the below-admin roles (moderator, creator).
	RolesGrantBasic authz.Permission = "oauth.roles.grant_basic"
	// RolesGrantSite: grant/revoke site-scoped roles (docs/integration/oauth/
	// 12-site-roles.md). Site roles are always below the global moderator, so an
	// admin may grant them.
	RolesGrantSite authz.Permission = "oauth.roles.grant_site"
	// RolesGrantAdmin: grant/revoke the admin-tier targets (admin, and the
	// implicit user base) — the ren-only half of the role-grant matrix.
	RolesGrantAdmin authz.Permission = "oauth.roles.grant_admin"
	// ClientsStorageConfig: enable a client's object-storage capabilities
	// (artifact_*/image_* columns) via PUT /oauth/clients/:id/storage.
	ClientsStorageConfig authz.Permission = "oauth.clients.storage_config"
	// ClientsPrivilegedConfig: set the sensitive client fields — ren-only
	// upload scopes (image:upload/artifact:upload), auto_consent, and the
	// cross-site display_order — on create or update.
	ClientsPrivilegedConfig authz.Permission = "oauth.clients.privileged_config"
	// SitesManageAll: see and edit EVERY site and OAuth client, not just the
	// ones the caller created. Without it an admin's console is scoped to their
	// own rows (sites.created_by_user_id / oauth_clients.created_by_user_id),
	// including rows created before ownership stamping existed (NULL creator —
	// they belong to nobody, so only this permission reaches them).
	SitesManageAll authz.Permission = "oauth.sites.manage_all"
	// PermissionsManage: run the permission console — grant/revoke overlay rows
	// on any role, for any live key the invariants allow. ren-only AND
	// non-delegable: holding it is holding the ability to hand out every other
	// key, so it can only ever move by a code change.
	PermissionsManage authz.Permission = "oauth.permissions.manage"
)

// CRUD keys for the console's two managed resources (sites, OAuth clients).
// oauth.admin_access stays the PAGE gate — it decides who reaches the console
// and sees the lists at all — and these decide who may mutate. They were split
// out of admin_access so a console seat can be handed to someone read-only, or
// to someone who may create but not delete, without minting a role.
//
// The per-row ownership rule (mayManage: manage_all sees everything, everyone
// else only their own rows) is UNCHANGED and applies on top: holding
// oauth.sites.update lets you update sites you own, not everyone's.
//
// No secret_regenerate key: the console has no secret-regeneration endpoint
// (a client secret is shown once at create time and never re-minted), so a key
// for it would gate nothing.
const (
	// SitesCreate: POST /sites.
	SitesCreate authz.Permission = "oauth.sites.create"
	// SitesUpdate: PUT /sites/:id.
	SitesUpdate authz.Permission = "oauth.sites.update"
	// SitesDelete: DELETE /sites/:id.
	SitesDelete authz.Permission = "oauth.sites.delete"
	// ClientsCreate: POST /oauth/clients.
	ClientsCreate authz.Permission = "oauth.clients.create"
	// ClientsUpdate: PUT /oauth/clients/:id (and the storage sub-resource,
	// which additionally needs oauth.clients.storage_config).
	ClientsUpdate authz.Permission = "oauth.clients.update"
	// ClientsDelete: DELETE /oauth/clients/:id.
	ClientsDelete authz.Permission = "oauth.clients.delete"
)

// NonDelegable are the console keys the overlay may never grant. Each one is a
// key whose holder could otherwise escalate past the console's own guardrails:
// grant_admin mints admins, permissions.manage rewrites the grant table itself,
// and sites.manage_all escapes per-row ownership scoping. Moving any of them
// requires editing this package — a reviewable diff, not a click.
var NonDelegable = authz.NonDelegable{
	RolesGrantAdmin:   true,
	PermissionsManage: true,
	SitesManageAll:    true,
}

// adminPerms is what an ordinary admin holds on the console: reach it, grant
// the below-admin roles, and perform the ordinary CRUD on the rows they own.
// Everything else is ren-only.
var adminPerms = []authz.Permission{
	AdminAccess,
	RolesGrantBasic,
	RolesGrantSite,
	SitesCreate,
	SitesUpdate,
	SitesDelete,
	ClientsCreate,
	ClientsUpdate,
	ClientsDelete,
}

// renPerms is admin's bundle plus the PII / admin-grant / privileged-client
// capabilities. Composing it from adminPerms keeps admin ⊆ ren by construction.
var renPerms = append(append([]authz.Permission{}, adminPerms...),
	UsersPIIView,
	RolesGrantAdmin,
	ClientsStorageConfig,
	ClientsPrivilegedConfig,
	SitesManageAll,
	PermissionsManage,
)

// Bundles is the console's role→permission table. moderator/creator have no
// console authority; `user` is implicit and grants nothing.
var Bundles = authz.Bundles{
	"admin": adminPerms,
	"ren":   renPerms,
}

// Resolver is the package-level Holder the console enforcement points check.
// It starts at the code bundles and is swapped whole when the permission
// overlay refreshes (docs/auth/04 §7).
var Resolver = authz.NewHolder(Bundles)
