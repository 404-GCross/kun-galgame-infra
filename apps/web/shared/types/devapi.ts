// Developer-platform (NextMoe open API) admin types.
//
// The management surface (cmd/oauth, /api/v1/admin/devapi/*) is a plain Fiber
// handler — NOT a Huma code-first surface — so there is no generated OpenAPI
// type. These shapes are hand-written to mirror the wire contract EXACTLY,
// field-for-field, from apps/api/internal/platform/devapi/handler.go
// (appView / keyView / mintedKeyView / patchAppRequest / mintKeyRequest).
// Keep in sync with that file. UI-only label/limit maps live in
// app/constants/devapi.ts.

// A dev-enabled application row (oauth_clients + its live key count).
// Mirrors handler.go `appView`.
export interface DevApp {
  client_id: string
  name: string
  owner_user_id?: number
  dev_enabled: boolean
  dev_tier: string
  dev_nsfw_allowed: boolean
  dev_rate_per_min: number
  dev_quota_daily: number
  key_count: number
}

// A developer API key (never carries plaintext or hash). Mirrors
// handler.go `keyView`. Timestamps are RFC3339 UTC strings; the optional ones
// are omitted (undefined) when unset server-side.
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
// (store/localStorage/URL/logs). Mirrors handler.go `mintedKeyView`.
export interface DevKeyMinted extends DevKey {
  key: string
}

// Request bodies (patchAppRequest / mintKeyRequest) are sent as inline
// Record<string, unknown> at the call sites, matching the repo convention
// (see catalog Candidates.vue) — useApi's body param is Record<string,
// unknown>, which a domain interface cannot satisfy without an index
// signature. The wire field names are asserted there against this contract.
