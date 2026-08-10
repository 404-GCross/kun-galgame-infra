
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

export const DEV_MINTABLE_SCOPES = ['catalog:read', 'galgame:read'] as const

export const MAX_APPS_PER_ACCOUNT = 5
export const MAX_ACTIVE_KEYS_PER_APP = 5

export const API_BASE_URL = 'https://api.nextmoe.dev/v1'

export const MCP_ENDPOINT = 'https://mcp.nextmoe.dev/mcp'
