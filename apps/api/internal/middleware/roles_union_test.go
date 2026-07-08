package middleware

import (
	"slices"
	"testing"

	catalogperm "api/internal/platform/catalog/perm"
	galgameperm "api/internal/platform/galgame/perm"
	siteperm "api/internal/platform/site/perm"
)

// TestUnionRoles pins the merge of global roles with the token's site_roles: the
// empty-site fast path returns global unchanged, site names are added, and
// overlaps dedupe.
func TestUnionRoles(t *testing.T) {
	cases := []struct {
		name         string
		global, site []string
		want         []string
	}{
		{"empty site returns global", []string{"creator"}, nil, []string{"creator"}},
		{"union adds site roles", []string{"creator"}, []string{"moderator"}, []string{"creator", "moderator"}},
		{"dedup overlapping", []string{"moderator"}, []string{"moderator"}, []string{"moderator"}},
		{"both empty", nil, nil, nil},
		{"only site roles", nil, []string{"event_organizer"}, []string{"event_organizer"}},
	}
	for _, tc := range cases {
		if got := unionRoles(tc.global, tc.site); !slices.Equal(got, tc.want) {
			t.Errorf("%s: unionRoles(%v, %v) = %v, want %v", tc.name, tc.global, tc.site, got, tc.want)
		}
	}
}

// TestSiteRolesCannotReachAdminBundles is the constructive safety assertion for
// the union-resolution design (docs 12 §5): because site_roles can never contain
// admin/ren (the grant API rejects those names), a unioned site-role set cannot
// grant any admin/ren-only permission — the catalog / oauth-console bundles key
// only on admin/ren, which a site role set structurally can't carry — while it
// DOES still grant the moderator-keyed capabilities (the intended effect).
func TestSiteRolesCannotReachAdminBundles(t *testing.T) {
	// A realistic site-role set: the grant policy guarantees it never contains
	// admin/ren. The union with an empty global set models a plain user who
	// only holds site roles.
	roles := unionRoles(nil, []string{"moderator", "event_organizer"})

	if catalogperm.Resolver.Can(roles, catalogperm.Review) {
		t.Error("site roles must not grant catalog.review (ren-only)")
	}
	if siteperm.Resolver.Can(roles, siteperm.AdminAccess) {
		t.Error("site roles must not grant oauth.admin_access (admin/ren)")
	}
	if siteperm.Resolver.Can(roles, siteperm.RolesGrantSite) {
		t.Error("site roles must not grant oauth.roles.grant_site (admin/ren)")
	}
	// The intended effect: a site "moderator" DOES grant galgame moderation.
	if !galgameperm.Resolver.Can(roles, galgameperm.Review) {
		t.Error("a site moderator should grant galgame.review")
	}
}
