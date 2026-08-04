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
)

// adminPerms is what an ordinary admin holds on the console: reach it, and
// grant the below-admin roles. Everything else is ren-only.
var adminPerms = []authz.Permission{
	AdminAccess,
	RolesGrantBasic,
	RolesGrantSite,
}

// renPerms is admin's bundle plus the PII / admin-grant / privileged-client
// capabilities. Composing it from adminPerms keeps admin ⊆ ren by construction.
var renPerms = append(append([]authz.Permission{}, adminPerms...),
	UsersPIIView,
	RolesGrantAdmin,
	ClientsStorageConfig,
	ClientsPrivilegedConfig,
	SitesManageAll,
)

// Bundles is the console's role→permission table. moderator/creator have no
// console authority; `user` is implicit and grants nothing.
var Bundles = authz.Bundles{
	"admin": adminPerms,
	"ren":   renPerms,
}

// Resolver is the package-level singleton the console enforcement points check.
var Resolver = authz.NewResolver(Bundles)
