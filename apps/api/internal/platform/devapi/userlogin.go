// userlogin.go — what a self-service application must satisfy before it may
// send a human to the NextMoe consent screen.
//
// Until now a developer-portal app was a pure API-key identity: `grants: []`
// and `redirect_uris: []`, fail-closed, unable to mint a user token at all.
// That was the right default while every open-API face was anonymous, and it
// is the wrong one the moment a face needs to know WHICH USER it is acting
// for — playtime being the first. The alternative was to keep minting those
// clients by hand in the OAuth console, which puts a human in the loop of
// every third-party integration forever.
//
// Opening self-service registration means an unvetted stranger can put an
// entry on our consent screen. The three checks in this file are what make
// that acceptable, and each one closes a specific documented attack:
//
//  1. Redirect-URI policy — an unconstrained redirect_uri turns the
//     authorization endpoint into an open redirector and, worse, into a
//     token-exfiltration channel.
//  2. Public-client + PKCE — a desktop launcher cannot keep a secret, so the
//     code it receives must be bound to a challenge only the running process
//     knows (RFC 8252 §8.1). The OAuth service already REFUSES a public
//     client's code without PKCE; this file's job is to make sure the app is
//     marked public in the first place.
//  3. Name policy — the consent screen shows the app's name next to the
//     user's account. "NextMoe 官方助手" on that screen is a credential
//     phishing page we hosted ourselves.
//
// The scope allow-list lives here too, and is deliberately SEPARATE from the
// API-key one: they govern different credentials. selfServiceScopes says what
// a machine key may do anonymously; selfServiceUserScopes says what an app may
// ASK A HUMAN FOR. Merging them would let a read-only key allow-list silently
// decide consent policy.
package devapi

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"

	siteModel "api/internal/platform/site/model"
)

// appAllowedScopes renders the oauth_clients.allowed_scopes value for a
// self-service app: always the two public API-key read scopes, plus the
// consent scopes the app declared (empty for a key-only app). One column
// carries both vocabularies because the OAuth service and the key resolver
// each filter it for their own credential — see the call site.
func appAllowedScopes(userScopes string) []byte {
	all := []string{ScopeCatalogRead, ScopeGalgameRead}
	all = append(all, strings.Fields(userScopes)...)
	encoded, err := json.Marshal(all)
	if err != nil {
		// Marshalling a []string cannot fail; the fallback keeps the column
		// NOT NULL and fail-closed rather than propagating an impossible error.
		return []byte(`["catalog:read","galgame:read"]`)
	}
	return encoded
}

// MaxRedirectURIsPerApp caps how many callbacks one app may register. Native
// apps need one loopback entry; a web app needs its production and staging
// origins. Five is generous for both and keeps the consent-time scan bounded.
const MaxRedirectURIsPerApp = 5

// selfServiceUserScopes is what a self-service app may put in front of a human.
//
// The three OIDC core scopes are here because an app that logs a user in needs
// to know who they are. playtime:read / playtime:write are here because that is
// the face this whole capability exists to unlock — and note what is NOT here:
// no catalog:edit, no image:upload, no artifact:upload. A self-service
// registration must never be able to request a scope that writes to the shared
// corpus or spends our storage. Adding one to this list is a policy decision,
// not a configuration change.
var selfServiceUserScopes = []string{
	"openid", "profile", "email",
	ScopePlaytimeRead, ScopePlaytimeWrite,
}

// The playtime scopes, named here because this package decides who may request
// them. They are consumed by the playtime face's gate in the catalog service —
// a USER-token scope granted through the consent flow, not an API-key scope.
const (
	ScopePlaytimeRead  = "playtime:read"
	ScopePlaytimeWrite = "playtime:write"
)

var (
	// ErrRedirectURIRequired — user login was requested with no callback.
	ErrRedirectURIRequired = errors.New("devapi: user login needs at least one redirect URI")
	// ErrTooManyRedirectURIs — over MaxRedirectURIsPerApp.
	ErrTooManyRedirectURIs = errors.New("devapi: too many redirect URIs (max 5)")
	// ErrRedirectURIInvalid — the URI is not a shape we will ever redirect to.
	ErrRedirectURIInvalid = errors.New("devapi: redirect URI must be https://, or http:// on the 127.0.0.1 / [::1] loopback for a native app")
	// ErrUserScopeNotAllowed — a requested consent scope is off the allow-list.
	ErrUserScopeNotAllowed = errors.New("devapi: scope not permitted for a self-service app (want openid/profile/email/playtime:read/playtime:write)")
	// ErrAppNameReserved — the name claims to be us.
	ErrAppNameReserved = errors.New("devapi: application name may not claim to be NextMoe or an official application")
)

// reservedNameFragments are the substrings a self-service app name may not
// contain. The check is on the CONSENT SCREEN's behalf: these are the words
// that make a user stop reading and click approve. Case-folded, and matched on
// the fragment rather than the whole name, because "NextMoe 助手" is exactly as
// misleading as "NextMoe".
//
// This is a floor, not a filter that catches everything — a determined
// impostor picks a homograph. It is paired with the third-party marking on the
// consent screen itself (owner_user_id != nil), which does not depend on
// guessing intent from a string.
var reservedNameFragments = []string{
	"nextmoe", "next moe", "未萌", "kungal", "kun galgame",
	"官方", "official", "admin", "管理员",
}

// UserLoginRequest is the self-service declaration that an app wants to log
// users in: where to send them back, and what to ask them for.
type UserLoginRequest struct {
	RedirectURIs []string
	Scopes       []string
}

// validateUserLogin checks a login declaration and returns the scope set to
// store, OIDC core scopes included. An app that asks for playtime but forgets
// `openid` still gets it — a token with no subject is useless to both sides,
// and refusing over it would be pedantry rather than safety.
func validateUserLogin(req UserLoginRequest) ([]string, error) {
	if len(req.RedirectURIs) == 0 {
		return nil, ErrRedirectURIRequired
	}
	if len(req.RedirectURIs) > MaxRedirectURIsPerApp {
		return nil, ErrTooManyRedirectURIs
	}
	for _, raw := range req.RedirectURIs {
		if err := validateRedirectURI(raw); err != nil {
			return nil, err
		}
	}
	scopes := []string{"openid"}
	for _, s := range req.Scopes {
		if !slices.Contains(selfServiceUserScopes, s) {
			return nil, ErrUserScopeNotAllowed
		}
		if !slices.Contains(scopes, s) {
			scopes = append(scopes, s)
		}
	}
	return scopes, nil
}

// validateRedirectURI applies the callback policy.
//
// Two shapes are accepted and nothing else:
//
//   - https://host/path — a web application's callback. TLS is not optional:
//     the authorization code travels in this URL.
//   - http://127.0.0.1/path or http://[::1]/path — a native application's
//     loopback callback (RFC 8252 §7.3). Plaintext is safe here and ONLY here,
//     because the request never leaves the user's machine.
//
// Everything else is refused, including the cases that look harmless:
// a fragment (it is the implicit flow's token channel), userinfo (it is how a
// crafted URI impersonates a host in a user's eyes), a wildcard (there is no
// such thing as a partially trusted redirect target), and http:// to any
// non-loopback host (a code in cleartext is a code anyone on the path holds).
// "localhost" is refused in favour of the literal addresses: it resolves
// through the host's name service and can be pointed elsewhere.
func validateRedirectURI(raw string) error {
	if strings.ContainsAny(raw, "*") || strings.TrimSpace(raw) != raw || raw == "" {
		return ErrRedirectURIInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Fragment != "" || strings.Contains(raw, "#") || u.User != nil {
		return ErrRedirectURIInvalid
	}
	host := u.Hostname()
	if host == "" {
		return ErrRedirectURIInvalid
	}
	switch u.Scheme {
	case "https":
		// A bare IP over https is legal but is never what a real deployment
		// looks like, and it defeats certificate-name reasoning for the human
		// reading the URL. Refused.
		if net.ParseIP(host) != nil {
			return ErrRedirectURIInvalid
		}
		return nil
	case "http":
		if isLoopbackHost(host) {
			return nil
		}
		return ErrRedirectURIInvalid
	}
	return ErrRedirectURIInvalid
}

// isLoopbackHost is the literal-address test — deliberately not a DNS
// resolution, and deliberately not "localhost".
func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// toUserLoginView renders an app's stored consent configuration, or nil when
// the app never declared user login. The API-key read scopes are filtered out
// of the scope list: they are the machine credential's vocabulary and would
// only confuse a developer reading "what will my users be asked for".
func toUserLoginView(app *siteModel.OAuthClient) *userLoginView {
	var uris []string
	if err := json.Unmarshal(app.RedirectURIs, &uris); err != nil || len(uris) == 0 {
		return nil
	}
	var all []string
	_ = json.Unmarshal(app.AllowedScopes, &all)
	consent := make([]string, 0, len(all))
	for _, s := range all {
		if s != ScopeCatalogRead && s != ScopeGalgameRead {
			consent = append(consent, s)
		}
	}
	return &userLoginView{RedirectURIs: uris, Scopes: consent, PKCERequired: true}
}

// validateAppName refuses a name that claims to be us. Applied to
// self-service apps only: a first-party client is created by an admin who is
// allowed to call it whatever it is.
func validateAppName(name string) error {
	folded := strings.ToLower(strings.ReplaceAll(name, " ", ""))
	for _, frag := range reservedNameFragments {
		if strings.Contains(folded, strings.ReplaceAll(frag, " ", "")) {
			return ErrAppNameReserved
		}
	}
	return nil
}
