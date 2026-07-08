package service

import (
	"strings"
	"testing"
)

// TestValidateSiteRoleName pins the grantable-name policy (docs 12 §3): the
// global-only names are rejected, and the pattern (lowercase, 2–50, letter-led)
// is enforced. Rejecting user/admin/ren is the load-bearing safety guard behind
// the union-resolution argument.
func TestValidateSiteRoleName(t *testing.T) {
	ok := []string{"moderator", "creator", "event_organizer", "ab", "a1", "team_lead_2"}
	for _, n := range ok {
		if err := validateSiteRoleName(n); err != nil {
			t.Errorf("validateSiteRoleName(%q) = %v, want nil", n, err)
		}
	}

	bad := []string{
		"user", "admin", "ren", // global-only — must never be a site role
		"Moderator",             // uppercase
		"1lead",                 // leading digit
		"_x",                    // leading underscore
		"a",                     // too short (needs ≥2)
		"",                      // empty
		"has-dash",              // dash not allowed
		strings.Repeat("a", 51), // too long (>50)
	}
	for _, n := range bad {
		if err := validateSiteRoleName(n); err == nil {
			t.Errorf("validateSiteRoleName(%q) = nil, want error", n)
		}
	}
}
