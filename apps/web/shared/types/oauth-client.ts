export interface OAuthClient {
  id: string
  site_id?: number
  name: string
  redirect_uris: string[]
  grants: string[]
  // OAuth scope allow-list. Empty/omitted at server side falls back to
  // the OIDC core set (openid/profile/email) — anything beyond that
  // (e.g. image:upload) MUST be listed explicitly.
  allowed_scopes?: string[]
  // RFC 6749 §2.1 public client (SPA / native). Public clients use
  // PKCE on the auth-code flow and skip client_secret on refresh.
  is_public?: boolean
  // First-party client flag. When true the OAuth web's /oauth/authorize
  // page silently skips the user consent UI for already-logged-in users
  // (silent SSO). Default false: third-party integrations always see
  // the manual consent screen. ENABLE ONLY for clients you fully own.
  // Policy: docs/integration/oauth/05-registration.md#auto_consent-字段语义.
  auto_consent?: boolean
  // Refresh token / session lifetime in seconds. Server default 90d.
  // Shorter for sensitive clients (e.g. 1d), longer for background services.
  refresh_token_ttl_seconds?: number
  // Public app-directory (生态一键登录) display fields. Plain presentation
  // metadata surfaced by GET /oauth/ecosystem — NOT ren-gated. `listed`
  // opt-in (default false); logo_url/tagline/display_order are display-only.
  // Contract: docs/integration/oauth/10-app-directory.md.
  listed?: boolean
  logo_url?: string
  tagline?: string
  display_order?: number
  created_at: string
  // Per-client object-storage capability config (artifact_*/image_* columns),
  // managed by the artifact admin "存储配置" page.
  storage?: OAuthClientStorageConfig
}

// Mirrors the backend OAuthClientStorageConfig (artifact_*/image_* columns).
export interface OAuthClientStorageConfig {
  artifact_enabled: boolean
  artifact_site_key: string
  artifact_cdn_base: string
  artifact_allowed_mime: string[]
  artifact_max_file_size: number
  artifact_quota_daily: number
  artifact_quota_bytes_daily: number
  image_enabled: boolean
  image_site_key: string
  image_cdn_base: string
  image_allowed_presets: string[]
  image_max_file_size: number
  image_quota_daily: number
  image_quota_bytes_daily: number
}

// Default refresh_token TTL when creating a new client (90 days in seconds).
// Matches model.OAuthClient's GORM default.
export const DEFAULT_REFRESH_TOKEN_TTL_SECONDS = 60 * 60 * 24 * 90

export interface OAuthClientCreated extends OAuthClient {
  secret: string
}

// One entry in the public app directory (GET /oauth/ecosystem) — the
// "拥有鲲 Galgame 账号，一键登录以下网站" strip. Only public display fields;
// mirrors the backend service.EcosystemApp. Contract:
// docs/integration/oauth/10-app-directory.md.
export interface EcosystemApp {
  name: string
  site_domain: string
  logo_url?: string
  tagline?: string
  // First-party ("官方") site — shown with an "官方" chip; sorts first.
  auto_consent: boolean
}

// Grant types supported by the server. Keep in sync with
// apps/api/internal/platform/auth/handler/oauth_handler.go Token().
export const ALL_GRANTS = ['authorization_code', 'refresh_token'] as const

// Scopes the server knows about. Used for the create/edit modal
// allow-list UI. Adding a new scope on the server should be paired
// with adding it here.
export const KNOWN_SCOPES = [
  'openid',
  'profile',
  'email',
  'image:upload',
  'artifact:upload',
] as const

// Sensitive scopes only the ren（莲）role may grant. The create/edit modals
// hide these from non-ren admins, and the server enforces the same gate
// (api .../site_handler.go renOnlyScopes). Granting artifact:upload turns on
// the whole artifact (large-file) capability for a client; both are default-off.
// Keep in sync with the backend.
export const REN_ONLY_SCOPES: readonly string[] = ['image:upload', 'artifact:upload']
