// Developer-platform (NextMoe open API) admin vocabulary: tier + scope display
// maps for the /devapi management pages. The tier names and per-tier default
// limits mirror apps/api/internal/platform/devapi/credential.go
// (TierFree/TierTrusted/TierInternal + TierLimits). API type shapes live in
// shared/types/devapi.ts.

export const DEV_TIERS = ['free', 'trusted', 'internal'] as const
export type DevTier = (typeof DEV_TIERS)[number]

// Tier picker options for KunSelect (value = wire string, label = display).
export const DEV_TIER_OPTIONS: { value: DevTier; label: string }[] = [
  { value: 'free', label: 'Free（免费）' },
  { value: 'trusted', label: 'Trusted（信任）' },
  { value: 'internal', label: 'Internal（内部，不限流）' },
]

// Chip color per tier for the app list.
export const DEV_TIER_COLORS: Record<string, 'default' | 'primary' | 'success'> = {
  free: 'default',
  trusted: 'primary',
  internal: 'success',
}

// Per-tier default per-minute rate + daily quota, mirroring devapi.TierLimits.
// `unlimited` = internal tier (neither limit enforced). Used for the "0 = 使用
// tier 默认" hint next to the override inputs.
export const DEV_TIER_LIMITS: Record<
  string,
  { rate: number; quota: number; unlimited: boolean }
> = {
  free: { rate: 60, quota: 50_000, unlimited: false },
  trusted: { rate: 600, quota: 1_000_000, unlimited: false },
  internal: { rate: 0, quota: 0, unlimited: true },
}

// Scopes offered in the mint dialog. Phase 1 issues only the two public-read
// scopes; galgame:nsfw is NOT offered (裁定 6 — NSFW scope not issued yet).
// Mirrors devapi.ScopeCatalogRead / ScopeGalgameRead.
export const DEV_MINTABLE_SCOPES = ['catalog:read', 'galgame:read'] as const

// A tier's human-readable default-limits hint.
export const devTierLimitHint = (tier: string): string => {
  const l = DEV_TIER_LIMITS[tier]
  if (!l) return ''
  if (l.unlimited) return '不限流 / 不限配额'
  return `${l.rate} 次/分 · ${l.quota.toLocaleString()} 次/日`
}
