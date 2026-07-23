// NextMoe developer portal wire types.
//
// The self-service surface (cmd/oauth, /api/v1/dev/*) is a plain Fiber handler —
// NOT a Huma code-first surface — so there is no generated OpenAPI type. These
// shapes are hand-written to mirror the wire contract EXACTLY, field-for-field,
// from the 06a self-service face (selfService `selfAppView` / `keyView` /
// `mintedKeyView` / `UsageDayFace`). Keep in sync with that handler.

// A developer application (a self-owned oauth_clients row + its live key count).
// The self-service face only ever returns tier=free; rate/quota are the EFFECTIVE
// values (tier default merged with any admin override; 0 = unlimited).
export interface DevApp {
  client_id: string
  name: string
  description: string
  dev_enabled: boolean
  tier: string
  rate_per_min: number
  quota_daily: number
  key_count: number
  created_at: string
}

// A developer API key (never carries plaintext or hash). Timestamps are RFC3339
// UTC strings; the optional ones are omitted (undefined) when unset server-side.
export interface DevKey {
  id: number
  client_id: string
  name: string
  key_prefix: string
  last4: string
  scopes: string[]
  nsfw_allowed: boolean
  expires_at?: string
  revoked_at?: string
  last_used_at?: string
  created_at: string
}

// The mint/rotate response: a key row plus the show-once plaintext. The `key`
// field appears exactly once, in this response, and MUST NEVER be persisted
// (store/localStorage/URL/logs).
export interface DevKeyMinted extends DevKey {
  key: string
}

// One aggregated usage row: (day, face) with request counts. face = catalog |
// galgame. Ordered day DESC, face ASC by the API.
export interface DevUsageDayFace {
  day: string
  face: string
  count: number
  status_4xx: number
  status_5xx: number
}

// One day's total usage across all of the account's apps (faces folded). The
// account-level series is DENSE (every day in the window, gaps 0-filled,
// oldest→newest) so the volume chart has a stable x-axis. day = YYYY-MM-DD UTC.
export interface DevUsageDay {
  day: string
  count: number
  status_4xx: number
  status_5xx: number
}

// One app's total usage over the window (named), for the per-app breakdown.
export interface DevUsageApp {
  client_id: string
  name: string
  count: number
  status_4xx: number
  status_5xx: number
}

// Account-level usage summary from GET /dev/usage?days=N — window totals, the
// dense daily series, and the per-app breakdown (sorted by volume).
export interface DevUsageSummary {
  days: number
  since: string
  total_count: number
  total_4xx: number
  total_5xx: number
  daily: DevUsageDay[]
  by_app: DevUsageApp[]
}

// The signed-in ecosystem account (subset of /auth/me we render in the portal).
export interface User {
  uuid: string
  name: string
  email: string
  avatar: string
  avatar_image_hash?: string | null
  bio?: string
  roles: string[]
  created_at: string
}

export interface LoginResponse {
  user: User
  access_token: string
}
