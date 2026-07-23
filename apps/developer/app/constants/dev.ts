// Developer-platform vocabulary for the portal: tier display + the per-tier
// default limits (mirrors apps/api/internal/platform/devapi/credential.go
// TierLimits) + the mintable scope allowlist. Wire type shapes live in
// shared/types/dev.ts.

// Chip color per tier. A self-service app is always `free`; trusted/internal are
// granted by an admin, but the portal renders whatever the API reports.
export const DEV_TIER_COLORS: Record<
  string,
  'default' | 'primary' | 'success'
> = {
  free: 'default',
  trusted: 'primary',
  internal: 'success'
}

export const DEV_TIER_LABELS: Record<string, string> = {
  free: 'Free（免费）',
  trusted: 'Trusted（信任）',
  internal: 'Internal（内部）'
}

// Scopes offered in the mint dialog. Phase 1 issues only the two public-read
// scopes; galgame:nsfw is NOT offered (裁定 6 — NSFW scope not issued yet).
export const DEV_MINTABLE_SCOPES = ['catalog:read', 'galgame:read'] as const

// Per-application limits from the design (§7). Every account starts on `free`.
export const MAX_APPS_PER_ACCOUNT = 5
export const MAX_ACTIVE_KEYS_PER_APP = 5

// The frozen public base URL third parties write into their code.
export const API_BASE_URL = 'https://api.nextmoe.dev/v1'

// The MCP (Model Context Protocol) endpoint — the SAME public /v1 read faces
// re-exposed as AI-agent tools over stateless Streamable HTTP. The same API key
// authenticates both; a tool call is billed to the key exactly like a direct
// /v1 request. See docs/developer-platform/09-mcp-server.md.
export const MCP_ENDPOINT = 'https://mcp.nextmoe.dev/mcp'
