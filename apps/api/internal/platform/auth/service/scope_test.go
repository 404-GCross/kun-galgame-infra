package service

import "testing"

func TestScopeGrants(t *testing.T) {
	cases := []struct {
		name  string
		scope string
		want  string
		grant bool
	}{
		{"empty scope grants everything", "", "email", true},
		{"whitespace-only scope grants everything", "   ", "email", true},

		{"granted email", "openid profile email", "email", true},
		{"granted profile", "openid profile email", "profile", true},

		{"email withheld when not requested", "openid profile", "email", false},
		{"openid alone grants no profile", "openid", "profile", false},

		{"prefix is not a match", "email_verified", "email", false},
		{"suffix is not a match", "not_email", "email", false},

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

func TestEmailForScope(t *testing.T) {
	const addr = "kun@kungal.com"

	cases := []struct {
		name  string
		scope string
		want  string
	}{
		{"email scope sees the address", "openid profile email", addr},
		{"email-only scope sees the address", "email", addr},

		{"profile-only scope is denied", "openid profile", ""},
		{"openid-only scope is denied", "openid", ""},

		{"scope-less first-party token sees the address", "", addr},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EmailForScope(tc.scope, addr); got != tc.want {
				t.Errorf("EmailForScope(%q, addr) = %q, want %q", tc.scope, got, tc.want)
			}
		})
	}
}
