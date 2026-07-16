package perm_test

import (
	"testing"

	"api/internal/platform/ai/perm"
	"api/internal/platform/authz"
)

// goldenGrants is the authoritative role-set for every AI permission. usage_view
// is an OPS capability: admin and ren reach the usage dashboard, moderator does
// NOT (usage cost is management, not content moderation). Any drift between the
// bundles and this table fails the build.
var goldenGrants = map[authz.Permission][]string{
	perm.UsageView: {"admin", "ren"},
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

// TestModeratorGrantsNothing pins the deliberate exclusion: a content moderator
// has no authority on the usage/cost surface (章程 步骤 02 §A).
func TestModeratorGrantsNothing(t *testing.T) {
	for p := range goldenGrants {
		if perm.Resolver.Can([]string{"moderator"}, p) {
			t.Errorf("moderator must grant nothing on the AI usage surface, but grants %q", p)
		}
	}
}

// TestNonBundleRolesGrantNothing pins the fail-closed default: any role outside
// the bundles grants nothing.
func TestNonBundleRolesGrantNothing(t *testing.T) {
	for _, role := range []string{"user", "creator", "", "legacy_top_tier_alias"} {
		for p := range goldenGrants {
			if perm.Resolver.Can([]string{role}, p) {
				t.Errorf("non-bundle role %q must grant nothing, but grants %q", role, p)
			}
		}
	}
}

// TestManagementAxisContainment pins admin ⊆ ren.
func TestManagementAxisContainment(t *testing.T) {
	for p := range goldenGrants {
		if perm.Resolver.Can([]string{"admin"}, p) && !perm.Resolver.Can([]string{"ren"}, p) {
			t.Errorf("ren must grant everything admin grants; missing %q", p)
		}
	}
}
