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
  created_at: string
}

// Default refresh_token TTL when creating a new client (90 days in seconds).
// Matches model.OAuthClient's GORM default.
export const DEFAULT_REFRESH_TOKEN_TTL_SECONDS = 60 * 60 * 24 * 90

export interface OAuthClientCreated extends OAuthClient {
  secret: string
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
