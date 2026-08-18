
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

export const DEV_MINTABLE_SCOPES = ['catalog:read'] as const

export const DEV_GRANTABLE_SCOPES = ['news:read'] as const

// Mirrors devapi's maxScopeAppMessageLen, which counts runes — so does the
// browser's maxlength (UTF-16 code units for BMP text), and the two agree for
// everything a form like this receives.
export const DEV_SCOPE_APP_MESSAGE_MAX = 2000

export const DEV_SCOPE_APP_STATUS_LABELS: Record<string, string> = {
  pending: '待审核',
  approved: '已批准',
  declined: '已拒绝'
}

export const DEV_SCOPE_APP_STATUS_COLORS: Record<
  string,
  'warning' | 'success' | 'danger'
> = {
  pending: 'warning',
  approved: 'success',
  declined: 'danger'
}

export const MAX_APPS_PER_ACCOUNT = 5
export const MAX_ACTIVE_KEYS_PER_APP = 5

export const API_BASE_URL = 'https://api.nextmoe.dev/v1'

export const MCP_ENDPOINT = 'https://mcp.nextmoe.dev/mcp'
