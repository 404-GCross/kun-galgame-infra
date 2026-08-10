
export const DEV_TIERS = ['free', 'trusted', 'internal'] as const
export type DevTier = (typeof DEV_TIERS)[number]

export const DEV_TIER_OPTIONS: { value: DevTier; label: string }[] = [
  { value: 'free', label: 'Free（免费）' },
  { value: 'trusted', label: 'Trusted（信任）' },
  { value: 'internal', label: 'Internal（内部，不限流）' },
]

export const DEV_TIER_COLORS: Record<string, 'default' | 'primary' | 'success'> = {
  free: 'default',
  trusted: 'primary',
  internal: 'success',
}

export const DEV_TIER_LIMITS: Record<
  string,
  { rate: number; quota: number; unlimited: boolean }
> = {
  free: { rate: 60, quota: 50_000, unlimited: false },
  trusted: { rate: 600, quota: 1_000_000, unlimited: false },
  internal: { rate: 0, quota: 0, unlimited: true },
}

export const DEV_MINTABLE_SCOPES = ['catalog:read', 'galgame:read'] as const

export const devTierLimitHint = (tier: string): string => {
  const l = DEV_TIER_LIMITS[tier]
  if (!l) return ''
  if (l.unlimited) return '不限流 / 不限配额'
  return `${l.rate} 次/分 · ${l.quota.toLocaleString()} 次/日`
}
