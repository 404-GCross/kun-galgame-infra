package perm_test

import (
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/catalog/perm"
)

// goldenGrants is the authoritative role-set for every catalog permission,
// derived from the pre-migration ren-only route gates. Any drift between the
// bundles and this table fails the build.
var goldenGrants = map[authz.Permission][]string{
	perm.Review: {"ren"},
	// Claim review is content moderation, not registry curation: it reaches
	// down to moderator so the wiki submission queue's staffing survives the
	// move onto the registry (wave 157).
	perm.ClaimReview:    {"moderator", "admin", "ren"},
	perm.EditWork:       {"admin", "ren"},
	perm.EditWorkReview: {"admin", "ren"},
	// The vocabulary layer follows the work keys: curation staff only. Tenant
	// users reach the editing engine through trust tiers and site overlays,
	// never through a global role.
	perm.EditTaxonomy:       {"admin", "ren"},
	perm.EditTaxonomyReview: {"admin", "ren"},
	// Writing at the trusted tier: staff only in CODE, because that is the
	// standing letmoe's retired S2S assertion gave them. It is deliberately
	// absent from moderator (trust is not moderation) — product sites grant it
	// to their own roles through the console overlay, which this table, being
	// the code bundles, does not and must not describe.
	perm.EditTrusted: {"admin", "ren"},
}

var allRoles = []string{"user", "creator", "moderator", "admin", "ren"}

func TestGoldenBundles(t *testing.T) {
	for p, granted := range goldenGrants {
		grantedSet := make(map[string]bool, len(granted))
		for _, r := range granted {
			grantedSet[r] = true
		}
		for _, role := range allRoles {
			want := grantedSet[role]
			if got := perm.Resolver.Can([]string{role}, p); got != want {
				t.Errorf("Can([%q], %q) = %v, want %v", role, p, got, want)
			}
		}
	}
}

// TestNonBundleRolesGrantNothing pins the fail-closed default: any role outside
// the bundles (implicit user, empty, or a retired alias) grants nothing.
func TestNonBundleRolesGrantNothing(t *testing.T) {
	for _, role := range []string{"user", "", "legacy_top_tier_alias"} {
		for p := range goldenGrants {
			if perm.Resolver.Can([]string{role}, p) {
				t.Errorf("non-bundle role %q must grant nothing, but grants %q", role, p)
			}
		}
	}
}

// TestManagementAxisContainment pins the contract's逐级包含: moderator ⊆ admin ⊆
// ren. Since wave 157 the moderator bundle is non-empty (catalog.claim.review),
// so this is a live assertion rather than a vacuous one: it fails the build if a
// moderator-visible permission is ever added without admin/ren inheriting it.
func TestManagementAxisContainment(t *testing.T) {
	for p := range goldenGrants {
		if perm.Resolver.Can([]string{"moderator"}, p) && !perm.Resolver.Can([]string{"admin"}, p) {
			t.Errorf("admin must grant everything moderator grants; missing %q", p)
		}
		if perm.Resolver.Can([]string{"admin"}, p) && !perm.Resolver.Can([]string{"ren"}, p) {
			t.Errorf("ren must grant everything admin grants; missing %q", p)
		}
	}
}

// TestCreatorGrantsNothing pins that creator (the publish axis) has no authority
// on this platform surface.
func TestCreatorGrantsNothing(t *testing.T) {
	for p := range goldenGrants {
		if perm.Resolver.Can([]string{"creator"}, p) {
			t.Errorf("creator must grant nothing on the catalog surface, but grants %q", p)
		}
	}
}
