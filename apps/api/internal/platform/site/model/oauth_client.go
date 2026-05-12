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
