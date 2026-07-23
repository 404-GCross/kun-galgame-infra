package model

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"gorm.io/datatypes"
)

// secretHashPrefix tags a hashed client secret so VerifySecret can tell a
// hashed value from a legacy plaintext one (both are 64 hex chars otherwise,
// so length/charset can't distinguish them).
const secretHashPrefix = "sha256:"

// HashOAuthClientSecret returns the at-rest form of a client secret:
// "sha256:<hex(sha256(secret))>". Client secrets are high-entropy server-
// generated random (32 bytes), so a fast cryptographic hash is sufficient and
// keeps per-request s2s auth cheap (unlike bcrypt). The plaintext is shown to
// the admin once at creation and never stored.
func HashOAuthClientSecret(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return secretHashPrefix + hex.EncodeToString(sum[:])
}

// VerifySecret constant-time compares a presented secret against the stored
// value. Hashed rows ("sha256:…") are compared by hash; legacy plaintext rows
// (pre-migration) fall back to a direct constant-time compare, so existing
// clients keep working until cmd/migrate-hash-client-secrets runs.
func (c *OAuthClient) VerifySecret(presented string) bool {
	stored := c.Secret
	if h, ok := strings.CutPrefix(stored, secretHashPrefix); ok {
		sum := sha256.Sum256([]byte(presented))
		return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(h)) == 1
	}
	// Legacy plaintext (not yet migrated).
	return subtle.ConstantTimeCompare([]byte(stored), []byte(presented)) == 1
}

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

	// MoemoepointAwarder authorizes this client to MINT moemoepoint via the
	// service-to-service POST /users/:id/moemoepoint endpoint. Reading the
	// balance (GET /users/:id/moemoepoint) and the admin grant path (admin
	// JWT) are NOT affected — this gate is only on downstream s2s awards.
	//
	// Default false (fail-closed): a newly-registered client can log users in
	// and read their balance, but CANNOT write to the shared unified-currency
	// wallet. Only the galgame-ecosystem apps that legitimately award points
	// (forum / patch) are flipped to true — cmd/migrate backfills them by
	// parent Site.Domain, never by hardcoded per-env client_id.
	//
	// Rationale: moemoepoint is ONE shared wallet across the whole ecosystem.
	// A content site with a different positioning (e.g. an adult-games site)
	// that seeds a local economy from this balance must not be able to mint
	// into the shared ledger — that would stamp its provenance onto every
	// user's wallet history. Read-to-seed is fine; minting is allow-listed.
	// Policy: docs/integration/oauth/06-moemoepoint.md §awarder allow-list.
	MoemoepointAwarder bool `gorm:"not null;default:false" json:"moemoepoint_awarder"`

	// --- App directory / ecosystem display (public, opt-in) ---
	// Listed surfaces this client in the public GET /oauth/ecosystem app
	// directory — the "one KUN Galgame account → these sites" strip on the
	// register/login screens. Opt-in: default false so internal/admin clients
	// stay out of the public list. LogoURL/Tagline/DisplayOrder are display-only.
	// See docs/integration/oauth/10-app-directory.md.
	Listed       bool   `gorm:"not null;default:false" json:"listed"`
	LogoURL      string `gorm:"size:255" json:"logo_url,omitempty"`
	Tagline      string `gorm:"size:100" json:"tagline,omitempty"`
	DisplayOrder int    `gorm:"not null;default:0" json:"display_order"`

	// --- Image service extension fields ---
	ImageEnabled bool   `gorm:"not null;default:false" json:"image_enabled"`
	ImageSiteKey string `gorm:"size:32" json:"image_site_key,omitempty"`

	// ImageCDNBase overrides the global image CDN base (KUN_IMAGE_CDN_BASE)
	// for THIS client's image URLs. Empty → global default. Lets a site serve
	// its images under its own domain (e.g. https://img.letmoe.com/img) off
	// the SAME content-addressed bucket. This is brand / domain-reputation /
	// payment-processor isolation, NOT content isolation: the same hash is
	// reachable under any site's domain (opaque hash keys, low discoverability)
	// — physical isolation would require a separate bucket. The image service
	// resolves this per request from the authenticated client; downstreams
	// that build URLs themselves should configure their imageclient CDNBase to
	// the same value. See docs/image_service.
	ImageCDNBase         string         `gorm:"size:255" json:"image_cdn_base,omitempty"`
	ImageQuotaDaily      int            `gorm:"default:10000" json:"image_quota_daily"`
	ImageQuotaBytesDaily int64          `gorm:"default:10737418240" json:"image_quota_bytes_daily"`
	ImageMaxFileSize     int64          `gorm:"default:10485760" json:"image_max_file_size"`
	ImageAllowedPresets  datatypes.JSON `gorm:"type:jsonb" json:"image_allowed_presets,omitempty"`

	// --- Artifact service extension fields ---
	// Mirror the Image* fields for the large-file artifact service. See
	// docs/artifact/02-storage-and-schema.md.
	ArtifactEnabled bool   `gorm:"not null;default:false" json:"artifact_enabled"`
	ArtifactSiteKey string `gorm:"size:32" json:"artifact_site_key,omitempty"`
	// ArtifactCDNBase is this site's Cloudflare-Worker download domain (e.g.
	// https://patch.touchgal.moe). Empty → downloads only via presigned GET.
	// When set AND an artifact is public, the download endpoint returns a
	// Worker-domain URL (cacheable, B2→CF egress free) instead of a presign.
	ArtifactCDNBase         string         `gorm:"size:255" json:"artifact_cdn_base,omitempty"`
	ArtifactQuotaDaily      int            `gorm:"default:1000" json:"artifact_quota_daily"`
	ArtifactQuotaBytesDaily int64          `gorm:"default:107374182400" json:"artifact_quota_bytes_daily"`
	ArtifactMaxFileSize     int64          `gorm:"default:21474836480" json:"artifact_max_file_size"`
	ArtifactAllowedMime     datatypes.JSON `gorm:"type:jsonb" json:"artifact_allowed_mime,omitempty"`

	// --- Developer platform / NextMoe open API extension fields ---
	// An application enrolled in the NextMoe open API is an oauth_clients row
	// with these platform-level fields (tier/quota apply to the whole open API,
	// not per-media — per-face access is expressed via scopes). Managed by the
	// developer-platform admin surface (cmd/oauth /admin/devapi). The columns
	// are added to the existing table by devapi.AddOAuthClientDevColumns (raw
	// SQL: NOT NULL backfilled then DROP DEFAULT), so the GORM tags below carry
	// NO `default:` — the zero value is written explicitly (intent-column
	// discipline, avoiding the GORM zero-value INSERT trap). See
	// docs/developer-platform/04-platform-internals.md §5.1.
	//
	// OwnerUserID owns a third-party developer application (ecosystem account);
	// NULL for first-party site clients (they belong via SiteID). Indexed for
	// the portal "my apps" list and management auth.
	OwnerUserID    *uint  `gorm:"index" json:"owner_user_id,omitempty"`
	DevEnabled     bool   `gorm:"not null" json:"dev_enabled"`      // admitted to the open API
	DevTier        string `gorm:"size:20;not null" json:"dev_tier"` // free|trusted|internal
	DevNSFWAllowed bool   `gorm:"not null" json:"dev_nsfw_allowed"`
	// Rate/quota overrides — 0 means "use the tier default" (see devapi §7).
	DevRatePerMin int `gorm:"not null" json:"dev_rate_per_min"`
	DevQuotaDaily int `gorm:"not null" json:"dev_quota_daily"`

	// --- Catalog service extension field ---
	// CatalogSite binds this client to a single catalog tenant/site key (the
	// ecosystem tenant key written into catalog_work.site — e.g. galgame_wiki
	// / kungal / moyu). It authorizes the S2S write path only: POST
	// /catalog/works/claim requires this to be non-empty AND equal to the
	// request's site, so a client can only claim works for the product it owns
	// (an unbound client cannot claim at all). Read faces (resolve, redirect
	// feed) are unaffected, as is the admin Bearer face. Nullable, NOT unique —
	// one site may have several clients. See docs/catalog.
	CatalogSite string `gorm:"size:64" json:"catalog_site,omitempty"`

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

// ArtifactAllowedMimes returns the parsed per-site allowlist of MIME types
// and/or file extensions for the artifact service. Empty/unset = allow all.
func (c *OAuthClient) ArtifactAllowedMimes() []string {
	if len(c.ArtifactAllowedMime) == 0 {
		return nil
	}
	var list []string
	if err := json.Unmarshal(c.ArtifactAllowedMime, &list); err != nil {
		return nil
	}
	return list
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
