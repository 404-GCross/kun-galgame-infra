package perm_test

import (
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/galgame/perm"
)

// goldenGrants is the authoritative role-set for every galgame permission. It
// is derived from the pre-migration role checks with the two contract-driven
// corrections applied (step-01 workflow §1): `ren` added to every set that
// contained `admin` (the management-axis containment the old role checks
// omitted), and the retired top-tier alias dropped everywhere (the IdP never
// issues it). Any drift between the bundles and this table fails the build.
var goldenGrants = map[authz.Permission][]string{
	perm.PublishDirect:   {"creator", "moderator", "admin", "ren"},
	perm.Review:          {"moderator", "admin", "ren"},
	perm.EditAny:         {"moderator", "admin", "ren"},
	perm.Create:          {"moderator", "admin", "ren"},
	perm.AdminAccess:     {"moderator", "admin", "ren"},
	perm.TaxonomyEditAny: {"moderator", "admin", "ren"},
	perm.TaxonomyReview:  {"moderator", "admin", "ren"},
	perm.SearchAllStates: {"moderator", "admin", "ren"},
	perm.OwnerOverride:   {"admin", "ren"},
	// Editing-engine keys (E2a): review follows the OwnerOverride axis,
	// status/vndb_id follow the management (moderator+) axis.
	perm.EditGameReview: {"admin", "ren"},
	perm.EditGameStatus: {"moderator", "admin", "ren"},
	perm.EditGameVNDBID: {"moderator", "admin", "ren"},
}

// allRoles is every role name the golden table is asserted against. It includes
// the four grantable roles plus the implicit `user` — which, being absent from
// the bundles, must grant nothing for every permission.
var allRoles = []string{"user", "creator", "moderator", "admin", "ren"}

// TestGoldenBundles pins the exact role → permission mapping: for every
// permission, each role either grants it or does not, matching goldenGrants.
func TestGoldenBundles(t *testing.T) {
	for p, granted := range goldenGrants {
		grantedSet := make(map[string]bool, len(granted))
		for _, r := range granted {
			grantedSet[r] = true
		}
		for _, role := range allRoles {
			want := grantedSet[role]
			got := perm.Resolver.Can([]string{role}, p)
			if got != want {
				t.Errorf("Can([%q], %q) = %v, want %v", role, p, got, want)
			}
		}
	}
}

// TestNonBundleRolesGrantNothing pins the fail-closed default: any role that is
// not one of the four grantable bundle roles — the implicit `user`, an empty
// string, or a retired top-tier alias — grants no galgame permission. This is
// what makes dropping the legacy alias safe: it is now just an unknown role.
func TestNonBundleRolesGrantNothing(t *testing.T) {
	nonBundle := []string{"user", "", "legacy_top_tier_alias"}
	for p := range goldenGrants {
		for _, role := range nonBundle {
			if perm.Resolver.Can([]string{role}, p) {
				t.Errorf("non-bundle role %q must grant nothing, but grants %q", role, p)
			}
		}
	}
}

// TestManagementAxisContainment is the property test for the contract's
// management-axis containment: anything moderator can do, admin can do;
// anything admin can do, ren can do (moderator ⊆ admin ⊆ ren).
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

// TestCreatorOrthogonal is the property test for the publish axis: creator
// grants exactly {publish_direct} and no management/review capability.
func TestCreatorOrthogonal(t *testing.T) {
	for p := range goldenGrants {
		got := perm.Resolver.Can([]string{"creator"}, p)
		want := p == perm.PublishDirect
		if got != want {
			t.Errorf("creator Can(%q) = %v, want %v (creator must grant only publish_direct)", p, got, want)
		}
	}
}
