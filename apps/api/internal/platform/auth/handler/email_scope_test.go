package handler

import "testing"

// TestVisibleEmail pins the /auth/me + PATCH /auth/me email gate: the endpoint
// used to hand the address to any valid token, which let a client granted only
// `openid profile` read what /oauth/userinfo withholds.
func TestVisibleEmail(t *testing.T) {
	const addr = "kun@kungal.com"

	cases := []struct {
		name  string
		scope string
		want  string
	}{
		{"email scope sees the address", "openid profile email", addr},
		{"email-only scope sees the address", "email", addr},

		// The tightening: no email scope -> empty, matching userinfo.
		{"profile-only scope is denied", "openid profile", ""},
		{"openid-only scope is denied", "openid", ""},

		// Account-center password logins negotiate no scope at all; the
		// settings page must keep showing the user their own address.
		{"scope-less first-party token sees the address", "", addr},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := visibleEmail(tc.scope, addr); got != tc.want {
				t.Errorf("visibleEmail(%q, addr) = %q, want %q", tc.scope, got, tc.want)
			}
		})
	}
}
