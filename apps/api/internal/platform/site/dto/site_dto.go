package dto

// CreateSiteRequest represents a site creation request
type CreateSiteRequest struct {
	Name        string `json:"name" validate:"required,max=50"`
	Domain      string `json:"domain" validate:"required,max=255"`
	Description string `json:"description"`
}

// UpdateSiteRequest represents a site update request
type UpdateSiteRequest struct {
	Name        *string `json:"name" validate:"omitempty,max=50"`
	Description *string `json:"description"`
}

// SiteResponse represents a site in API responses
type SiteResponse struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Domain      string `json:"domain"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// CreateOAuthClientRequest represents an OAuth client creation request.
//
// AllowedScopes is the OAuth scope allow-list (e.g.
// `["openid","profile","email","image:upload"]`). Empty/omitted is
// equivalent to the OIDC core set {openid, profile, email} only —
// sensitive scopes like `image:upload` MUST be explicitly listed.
//
// IsPublic marks the client as a public (SPA / native) client. Public
// clients must use PKCE on the auth-code flow and skip client_secret
// on refresh; see model.OAuthClient.IsPublic.
//
// RefreshTokenTTLSeconds is the refresh_token / session lifetime in
// seconds. Nil → server uses model default (90d). Provide a custom
// value for high-sensitivity clients (shorter) or background services
// (longer).
type CreateOAuthClientRequest struct {
	SiteID                 uint     `json:"site_id" validate:"required"`
	Name                   string   `json:"name" validate:"required,max=100"`
	RedirectURIs           []string `json:"redirect_uris" validate:"required,min=1"`
	// omitempty preserves nil-vs-set: omitting grants falls through to the
	// handler default; a non-nil value must be a non-empty subset of the
	// known grants (rejects [] → a permanently-unusable client, and unknown
	// strings like "password").
	Grants                 []string `json:"grants" validate:"omitempty,min=1,dive,oneof=authorization_code refresh_token"`
	AllowedScopes          []string `json:"allowed_scopes"`
	IsPublic               bool     `json:"is_public"`
	// AutoConsent marks the client as first-party for the OAuth web
	// `/oauth/authorize` page — when true, the consent UI is silently
	// skipped on user-already-logged-in path. Default false: third-
	// party integrations always see the consent screen.
	// Toggle this ONLY for clients you control end-to-end. Per-client
	// semantics: model.OAuthClient.AutoConsent (auth_clients column).
	AutoConsent            bool     `json:"auto_consent"`
	RefreshTokenTTLSeconds *int     `json:"refresh_token_ttl_seconds" validate:"omitempty,min=60"`
	// App-directory (public, opt-in) display fields surfaced by
	// GET /oauth/ecosystem. listed (default false)/logo_url/tagline are
	// admin-settable; display_order is ren-only (cross-site ordering — pinned to
	// 0 for non-ren in the handler). See docs/integration/oauth/10-app-directory.md.
	Listed       bool   `json:"listed"`
	LogoURL      string `json:"logo_url" validate:"omitempty,url,max=255"`
	Tagline      string `json:"tagline" validate:"omitempty,max=100"`
	DisplayOrder int    `json:"display_order" validate:"omitempty,min=0"`
}

// UpdateOAuthClientRequest represents an OAuth client update request.
// AllowedScopes / RefreshTokenTTLSeconds follow nil-vs-empty semantics:
// nil = leave alone, non-nil = set.
//
// IsPublic is intentionally NOT updatable here — switching a client
// between public/confidential changes its auth model and invalidates
// the security assumptions of currently-active tokens. Recreate the
// client instead.
type UpdateOAuthClientRequest struct {
	Name                   *string  `json:"name" validate:"omitempty,max=100"`
	RedirectURIs           []string `json:"redirect_uris" validate:"omitempty,min=1"`
	// Same membership/non-empty guard as CreateOAuthClientRequest.Grants;
	// omitting it still means "leave alone".
	Grants                 []string `json:"grants" validate:"omitempty,min=1,dive,oneof=authorization_code refresh_token"`
	AllowedScopes          []string `json:"allowed_scopes"`
	// Pointer-presence: nil = leave alone; non-nil = set explicitly.
	// Same flag as CreateOAuthClientRequest.AutoConsent — toggles whether
	// /oauth/authorize silently consents for this client's users.
	AutoConsent            *bool    `json:"auto_consent"`
	RefreshTokenTTLSeconds *int     `json:"refresh_token_ttl_seconds" validate:"omitempty,min=60"`
	// App-directory display fields. PATCH-style: nil = leave alone, non-nil
	// = set. listed/logo_url/tagline are admin-settable; display_order is
	// ren-only (forced to nil = unchanged for non-ren in the handler).
	// See docs/integration/oauth/10-app-directory.md.
	Listed       *bool   `json:"listed"`
	LogoURL      *string `json:"logo_url" validate:"omitempty,url,max=255"`
	Tagline      *string `json:"tagline" validate:"omitempty,max=100"`
	DisplayOrder *int    `json:"display_order" validate:"omitempty,min=0"`
}

// OAuthClientResponse represents an OAuth client in API responses
type OAuthClientResponse struct {
	ID                     string   `json:"id"`
	SiteID                 *uint    `json:"site_id,omitempty"`
	Name                   string   `json:"name"`
	RedirectURIs           []string `json:"redirect_uris"`
	Grants                 []string `json:"grants"`
	AllowedScopes          []string `json:"allowed_scopes"`
	IsPublic               bool     `json:"is_public"`
	AutoConsent            bool     `json:"auto_consent"`
	RefreshTokenTTLSeconds int      `json:"refresh_token_ttl_seconds"`
	// App-directory (public, opt-in) display fields.
	Listed       bool   `json:"listed"`
	LogoURL      string `json:"logo_url"`
	Tagline      string `json:"tagline"`
	DisplayOrder int    `json:"display_order"`
	CreatedAt    string `json:"created_at"`
	// Storage holds the per-client object-storage capability config (the
	// artifact_*/image_* columns), surfaced for the admin storage-config page.
	Storage OAuthClientStorageConfig `json:"storage"`
}

// OAuthClientStorageConfig mirrors the artifact_*/image_* columns on an OAuth
// client. JSON-list columns (allowed mime/presets) are decoded to []string.
type OAuthClientStorageConfig struct {
	ArtifactEnabled         bool     `json:"artifact_enabled"`
	ArtifactSiteKey         string   `json:"artifact_site_key"`
	ArtifactCDNBase         string   `json:"artifact_cdn_base"`
	ArtifactAllowedMime     []string `json:"artifact_allowed_mime"`
	ArtifactMaxFileSize     int64    `json:"artifact_max_file_size"`
	ArtifactQuotaDaily      int      `json:"artifact_quota_daily"`
	ArtifactQuotaBytesDaily int64    `json:"artifact_quota_bytes_daily"`
	ImageEnabled            bool     `json:"image_enabled"`
	ImageSiteKey            string   `json:"image_site_key"`
	ImageCDNBase            string   `json:"image_cdn_base"`
	ImageAllowedPresets     []string `json:"image_allowed_presets"`
	ImageMaxFileSize        int64    `json:"image_max_file_size"`
	ImageQuotaDaily         int      `json:"image_quota_daily"`
	ImageQuotaBytesDaily    int64    `json:"image_quota_bytes_daily"`
}

// UpdateClientStorageRequest is the admin storage-config form payload. It always
// carries the full state (the form loads current values and re-submits all), so
// fields are non-pointer — an omitted field reads as its zero value.
type UpdateClientStorageRequest struct {
	ArtifactEnabled         bool     `json:"artifact_enabled"`
	ArtifactSiteKey         string   `json:"artifact_site_key" validate:"max=32"`
	ArtifactCDNBase         string   `json:"artifact_cdn_base" validate:"max=255"`
	ArtifactAllowedMime     []string `json:"artifact_allowed_mime"`
	ArtifactMaxFileSize     int64    `json:"artifact_max_file_size" validate:"min=0"`
	ArtifactQuotaDaily      int      `json:"artifact_quota_daily" validate:"min=0"`
	ArtifactQuotaBytesDaily int64    `json:"artifact_quota_bytes_daily" validate:"min=0"`
	ImageEnabled            bool     `json:"image_enabled"`
	ImageSiteKey            string   `json:"image_site_key" validate:"max=32"`
	ImageCDNBase            string   `json:"image_cdn_base" validate:"max=255"`
	ImageAllowedPresets     []string `json:"image_allowed_presets"`
	ImageMaxFileSize        int64    `json:"image_max_file_size" validate:"min=0"`
	ImageQuotaDaily         int      `json:"image_quota_daily" validate:"min=0"`
	ImageQuotaBytesDaily    int64    `json:"image_quota_bytes_daily" validate:"min=0"`
}

// OAuthClientCreatedResponse includes the secret (only shown once on creation)
type OAuthClientCreatedResponse struct {
	OAuthClientResponse
	Secret string `json:"secret"`
}
