// userlogin_test.go — the policy that stands between "anyone can register an
// app" and "anyone can put a page in front of our users". Every case here is a
// thing an attacker would try, or a thing a legitimate native app must be able
// to do.
package devapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRedirectURI(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		ok   bool
	}{
		// The two shapes that exist for a reason.
		{"an https callback", "https://manager.example.com/oauth/callback", true},
		{"an https callback with a query", "https://example.com/cb?app=1", true},
		{"a native loopback callback", "http://127.0.0.1:53682/callback", true},
		{"the IPv6 loopback", "http://[::1]:53682/callback", true},
		{"a loopback with no port yet", "http://127.0.0.1/callback", true},

		// The code travels in this URL; cleartext to anywhere but the user's
		// own machine hands it to whoever is on the path.
		{"plain http to a real host", "http://manager.example.com/cb", false},
		// Resolves through the host's name service, so it is not necessarily
		// the local machine — 127.0.0.1 cannot be redirected.
		{"localhost by name", "http://localhost:53682/callback", false},
		// The fragment is the implicit flow's token channel.
		{"a fragment", "https://example.com/cb#token", false},
		// "Partially trusted redirect target" is not a thing.
		{"a wildcard host", "https://*.example.com/cb", false},
		{"a wildcard path", "https://example.com/*", false},
		// Reads as example.com to a human, resolves to evil.com.
		{"userinfo impersonating a host", "https://example.com@evil.com/cb", false},
		{"a relative URI", "/callback", false},
		{"a non-http scheme", "javascript:alert(1)", false},
		{"empty", "", false},
		{"whitespace padding", " https://example.com/cb ", false},
		// Legal, but defeats every name-based judgement a human makes about a
		// URL, and no real deployment looks like this.
		{"https to a bare IP", "https://93.184.216.34/cb", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateRedirectURI(c.uri)
			if c.ok {
				assert.NoError(t, err, "%q must be accepted", c.uri)
				return
			}
			assert.ErrorIs(t, err, ErrRedirectURIInvalid, "%q must be refused", c.uri)
		})
	}
}

func TestValidateUserLogin(t *testing.T) {
	good := []string{"http://127.0.0.1:1/cb"}

	t.Run("openid is added for an app that forgot it", func(t *testing.T) {
		scopes, err := validateUserLogin(UserLoginRequest{
			RedirectURIs: good, Scopes: []string{ScopePlaytimeWrite},
		})
		require.NoError(t, err)
		assert.Contains(t, scopes, "openid")
		assert.Contains(t, scopes, ScopePlaytimeWrite)
	})

	t.Run("a scope off the allow-list is refused", func(t *testing.T) {
		// catalog:edit is the one that matters: a self-service registration
		// must never be able to ask a human for write access to the corpus.
		for _, scope := range []string{"catalog:edit", "image:upload", "artifact:upload", "galgame:nsfw"} {
			_, err := validateUserLogin(UserLoginRequest{RedirectURIs: good, Scopes: []string{scope}})
			assert.ErrorIs(t, err, ErrUserScopeNotAllowed, "scope %q", scope)
		}
	})

	t.Run("no callback at all", func(t *testing.T) {
		_, err := validateUserLogin(UserLoginRequest{Scopes: []string{ScopePlaytimeRead}})
		assert.ErrorIs(t, err, ErrRedirectURIRequired)
	})

	t.Run("over the callback cap", func(t *testing.T) {
		many := make([]string, MaxRedirectURIsPerApp+1)
		for i := range many {
			many[i] = "https://example.com/cb"
		}
		_, err := validateUserLogin(UserLoginRequest{RedirectURIs: many})
		assert.ErrorIs(t, err, ErrTooManyRedirectURIs)
	})

	t.Run("one bad callback poisons the set", func(t *testing.T) {
		_, err := validateUserLogin(UserLoginRequest{
			RedirectURIs: []string{"https://example.com/cb", "http://evil.example.com/cb"},
		})
		assert.ErrorIs(t, err, ErrRedirectURIInvalid)
	})

	t.Run("duplicate scopes collapse", func(t *testing.T) {
		scopes, err := validateUserLogin(UserLoginRequest{
			RedirectURIs: good,
			Scopes:       []string{"openid", "openid", ScopePlaytimeRead, ScopePlaytimeRead},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"openid", ScopePlaytimeRead}, scopes)
	})
}

// The consent screen shows this name beside the user's account. These are the
// strings that stop a person from reading the rest of the page.
func TestValidateAppName(t *testing.T) {
	for _, name := range []string{
		"NextMoe", "nextmoe helper", "NextMoe 官方助手", "未萌启动器",
		"KunGal Manager", "官方管理器", "Official Sync", "ADMIN tools",
		"Next Moe Sync",
	} {
		assert.ErrorIs(t, validateAppName(name), ErrAppNameReserved, "%q", name)
	}
	for _, name := range []string{
		"Kurumi", "GalGame Manager", "我的游戏库", "VN Launcher",
	} {
		assert.NoError(t, validateAppName(name), "%q", name)
	}
}

// A key-only app keeps exactly the shape it had before user login existed —
// the read scopes and nothing else.
func TestAppAllowedScopesKeyOnly(t *testing.T) {
	assert.JSONEq(t, `["catalog:read","galgame:read"]`, string(appAllowedScopes("")))
}

func TestAppAllowedScopesWithConsent(t *testing.T) {
	assert.JSONEq(t,
		`["catalog:read","galgame:read","openid","playtime:write"]`,
		string(appAllowedScopes("openid playtime:write")))
}
