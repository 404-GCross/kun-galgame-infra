package service

import (
	"strings"
	"testing"
)

func TestValidateSiteRoleName(t *testing.T) {
	ok := []string{"moderator", "creator", "event_organizer", "ab", "a1", "team_lead_2"}
	for _, n := range ok {
		if err := validateSiteRoleName(n); err != nil {
			t.Errorf("validateSiteRoleName(%q) = %v, want nil", n, err)
		}
	}

	bad := []string{
		"user", "admin", "ren",
		"Moderator",
		"1lead",
		"_x",
		"a",
		"",
		"has-dash",
		strings.Repeat("a", 51),
	}
	for _, n := range bad {
		if err := validateSiteRoleName(n); err == nil {
			t.Errorf("validateSiteRoleName(%q) = nil, want error", n)
		}
	}
}
