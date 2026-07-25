package service

import "testing"

// ScopeGrants is the single rule behind both /oauth/userinfo's field filter and
// /auth/me's email gate, so its edge cases are pinned here rather than in either
// caller.
func TestScopeGrants(t *testing.T) {
	cases := []struct {
		name  string
		scope string
		want  string
		grant bool
	}{
		// An empty scope is a first-party password-login token (no
		// authorization-code grant ever negotiated a scope) — everything.
		{"empty scope grants everything", "", "email", true},
		{"whitespace-only scope grants everything", "   ", "email", true},

		{"granted email", "openid profile email", "email", true},
		{"granted profile", "openid profile email", "profile", true},

		// The bug third parties hit: `openid profile` never yields email.
		{"email withheld when not requested", "openid profile", "email", false},
		{"openid alone grants no profile", "openid", "profile", false},

		// Substring must not satisfy the check — scope tokens are whole words.
		{"prefix is not a match", "email_verified", "email", false},
		{"suffix is not a match", "not_email", "email", false},

		// Tabs / repeated spaces are legal whitespace in a scope string.
		{"tab-separated", "openid\temail", "email", true},
		{"double-spaced", "openid  email", "email", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScopeGrants(tc.scope, tc.want); got != tc.grant {
				t.Errorf("ScopeGrants(%q, %q) = %v, want %v", tc.scope, tc.want, got, tc.grant)
			}
		})
	}
}
