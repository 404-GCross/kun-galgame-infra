package perm_test

import (
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/galgame/perm"
)

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
	perm.EditGameReview:  {"admin", "ren"},
	perm.EditGameStatus:  {"moderator", "admin", "ren"},
	perm.EditGameVNDBID:  {"moderator", "admin", "ren"},
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
			got := perm.Resolver.Can([]string{role}, p)
			if got != want {
				t.Errorf("Can([%q], %q) = %v, want %v", role, p, got, want)
			}
		}
	}
}

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

func TestCreatorOrthogonal(t *testing.T) {
	for p := range goldenGrants {
		got := perm.Resolver.Can([]string{"creator"}, p)
		want := p == perm.PublishDirect
		if got != want {
			t.Errorf("creator Can(%q) = %v, want %v (creator must grant only publish_direct)", p, got, want)
		}
	}
}
