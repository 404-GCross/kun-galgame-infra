package model

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"gorm.io/datatypes"
)

// OAuthClient represents an OAuth client for a site
type OAuthClient struct {
	ID           string         `gorm:"size:50;primaryKey" json:"id"`
	SiteID       *uint          `gorm:"index" json:"site_id,omitempty"`
	Name         string         `gorm:"size:100;not null" json:"name"`
	Secret       string         `gorm:"size:255;not null" json:"-"`
	RedirectURIs datatypes.JSON `gorm:"type:jsonb;not null" json:"redirect_uris"`
	Grants       datatypes.JSON `gorm:"type:jsonb;not null" json:"grants"`
	CreatedAt    time.Time      `json:"created_at"`

	// IsPublic marks this client as a public OAuth client (RFC 6749 §2.1):
	// a SPA / native app that cannot securely hold a client_secret.
	// Such clients MUST use PKCE on the authorization-code flow, and the
	// /oauth/token refresh_token grant accepts them without client_secret
	// (the refresh_token itself is the proof of authorization).
	//
	// Default false — existing confidential clients keep requiring their
	// secret on every grant. Flip to true for SPA clients like wiki.
	IsPublic bool `gorm:"not null;default:false" json:"is_public"`

	// AutoConsent marks this client as first-party — the OAuth web frontend
	// skips the "Authorize this app to access X?" UI and silently posts
	// /oauth/authorize/consent on the user's behalf. Matches the SSO
	// expectation for kungal / moyu / wiki etc. that share the same owner
	// as the OAuth server: nobody benefits from showing a consent screen
	// for "kungal wants to access your kungal account" — it's friction
	// without information.
	//
	// Returned from GET /oauth/client-info so the frontend can decide
	// whether to render the consent card on the /oauth/authorize page.
	// The decision is frontend-only — backend always issues the code
	// when POST /oauth/authorize/consent is called with a valid session,
	// regardless of this flag.
	//
	// Default false — third-party integrations always see the consent
	// page. Flip to true only for clients you control end-to-end.
	// Policy + downstream pattern: docs/integration/oauth/05-registration.md.
	AutoConsent bool `gorm:"not null;default:false" json:"auto_consent"`

	// RefreshTokenTTLSeconds — how long this client's refresh_token (and
	// its associated session row) remains valid. Used as the refresh_token
	// JWT `exp` claim AND as session.ExpiresAt at issuance time.
	//
	// Default: 7,776,000 seconds = 90 days. Reasonable balance for typical
	// browser sessions — long enough that users don't get bumped while
	// active, short enough that abandoned sessions naturally expire.
	//
	// Per-client override lets sensitive clients (e.g. admin tooling)
	// run with a much shorter window (e.g. 1 day), and long-lived
	// background services run longer (e.g. 1 year).
	//
	// 0 or negative is treated as "use the model default" via
	// RefreshTokenTTL() so old rows without an explicit value still work.
	RefreshTokenTTLSeconds int `gorm:"not null;default:7776000" json:"refresh_token_ttl_seconds"`

	// AllowedScopes is the explicit allow-list of OAuth scopes this
	// client may request at /oauth/authorize. Stored as a JSON string
	// array (e.g. `["openid","profile","email","image:upload"]`).
	//
	// Enforcement: CreateAuthorizationCode splits the requested scope on
	// whitespace and verifies every token is present in this list.
	// Missing tokens → ErrOAuthInvalidScope, no code issued.
	//
	// Empty / null is NOT "allow anything". To keep pre-allowlist
	// deployments working without an emergency admin pass over every
	// client, an empty list is treated as "OIDC core only" — exactly
	// {openid, profile, email}. Anything outside that (e.g.
	// `image:upload`) MUST be explicitly listed.
	AllowedScopes datatypes.JSON `gorm:"type:jsonb" json:"allowed_scopes,omitempty"`

	// --- Image service extension fields ---
	ImageEnabled          bool           `gorm:"not null;default:false" json:"image_enabled"`
	ImageSiteKey          string         `gorm:"size:32" json:"image_site_key,omitempty"`
	ImageQuotaDaily       int            `gorm:"default:10000" json:"image_quota_daily"`
	ImageQuotaBytesDaily  int64          `gorm:"default:10737418240" json:"image_quota_bytes_daily"`
	ImageMaxFileSize      int64          `gorm:"default:10485760" json:"image_max_file_size"`
	ImageAllowedPresets   datatypes.JSON `gorm:"type:jsonb" json:"image_allowed_presets,omitempty"`

	// Relations
	Site *Site `gorm:"foreignKey:SiteID" json:"site,omitempty"`
}

// TableName returns the table name for OAuthClient
func (OAuthClient) TableName() string {
	return "oauth_clients"
}

// IsActive checks if the OAuth client is active
func (c *OAuthClient) IsActive() bool {
	return true // All clients are active by default
}

// HasRedirectURI checks if the given redirect URI is allowed
func (c *OAuthClient) HasRedirectURI(uri string) bool {
	var uris []string
	if err := json.Unmarshal(c.RedirectURIs, &uris); err != nil {
		return false
	}
	return slices.Contains(uris, uri)
}

// AllowedPresets returns the parsed list of image preset names this client
// can use. Returns nil/empty if the field is not set.
func (c *OAuthClient) AllowedPresets() []string {
	if len(c.ImageAllowedPresets) == 0 {
		return nil
	}
	var presets []string
	if err := json.Unmarshal(c.ImageAllowedPresets, &presets); err != nil {
		return nil
	}
	return presets
}

// IsPresetAllowed checks if the given preset is in AllowedPresets.
func (c *OAuthClient) IsPresetAllowed(preset string) bool {
	return slices.Contains(c.AllowedPresets(), preset)
}

// oidcCoreScopes are the scopes implicitly granted to any client when
// AllowedScopes is empty (legacy compatibility for pre-allowlist clients).
// Sensitive scopes (image:upload, etc.) are deliberately excluded.
var oidcCoreScopes = []string{"openid", "profile", "email"}

// allowedScopeList returns the resolved scope allowlist for this client:
//   - if AllowedScopes is non-empty: parse and return as-is
//   - else: return the OIDC core scopes as a safe default
func (c *OAuthClient) allowedScopeList() []string {
	if len(c.AllowedScopes) == 0 {
		return oidcCoreScopes
	}
	var scopes []string
	if err := json.Unmarshal(c.AllowedScopes, &scopes); err != nil || len(scopes) == 0 {
		return oidcCoreScopes
	}
	return scopes
}

// CheckScope validates that every token in the whitespace-separated
// `scope` string is allowed for this client. Returns the first
// disallowed scope token (for diagnostic logging), or "" on success.
// Empty `scope` is always allowed — that's "no extra permissions
// requested beyond the bearer identity itself".
func (c *OAuthClient) CheckScope(scope string) (disallowed string, ok bool) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "", true
	}
	allowed := c.allowedScopeList()
	for _, tok := range strings.Fields(scope) {
		if !slices.Contains(allowed, tok) {
			return tok, false
		}
	}
	return "", true
}

// AllowedGrants returns the parsed grant-type allow-list for this client.
// Returns nil if the column is empty / malformed — callers should treat
// nil as "no grants allowed" and reject everything (fail-closed).
func (c *OAuthClient) AllowedGrants() []string {
	if len(c.Grants) == 0 {
		return nil
	}
	var grants []string
	if err := json.Unmarshal(c.Grants, &grants); err != nil {
		return nil
	}
	return grants
}

// IsGrantAllowed reports whether the given grant_type is registered on
// this client. /oauth/token must reject grants not in this list — e.g.
// a client created with grants=["authorization_code"] should not be
// able to mint tokens via refresh_token grant.
//
// Fail-closed: empty/malformed Grants column means no grants allowed.
// (Existing clients in the DB always have at least
// ["authorization_code"] from the admin-create path, so this is the
// safe default for newly-introduced clients with no UI knowledge.)
func (c *OAuthClient) IsGrantAllowed(grantType string) bool {
	return slices.Contains(c.AllowedGrants(), grantType)
}

// defaultRefreshTokenTTL is the fallback when a client row has 0 or
// negative RefreshTokenTTLSeconds (e.g. pre-migration rows where the
// column doesn't exist yet). 90 days — matches the default written to
// new rows by the DB schema.
const defaultRefreshTokenTTL = 90 * 24 * time.Hour

// RefreshTokenTTL returns the refresh_token lifetime for this client
// as a time.Duration. Used at issuance and refresh time to set both
// the refresh_token JWT exp claim and the corresponding session row's
// expires_at column.
func (c *OAuthClient) RefreshTokenTTL() time.Duration {
	if c.RefreshTokenTTLSeconds <= 0 {
		return defaultRefreshTokenTTL
	}
	return time.Duration(c.RefreshTokenTTLSeconds) * time.Second
}
