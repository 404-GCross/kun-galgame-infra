// AI-gateway usage-dashboard vocabulary: display labels + semantic colors for
// the wire constants of the AI admin API (docs/ai/admin-openapi.yaml). The
// status numbers mirror apps/api/internal/platform/ai/model/constants.go and are
// persisted — never renumber.

// Semantic chip colors (mirrors @kungal/ui-core's KunUIColor union — the project
// palette only, never Tailwind built-ins).
export type AiChipColor =
  | 'default'
  | 'primary'
  | 'secondary'
  | 'success'
  | 'warning'
  | 'danger'
  | 'info'

// Semantic route names (mirrors model.RouteModerateText). v0 ships only
// moderate-text; the budget form defaults to it.
export const AI_ROUTE_MODERATE_TEXT = 'moderate-text'

// Aggregation windows offered by /admin/ai/usage/summary.
export const AI_WINDOW_OPTIONS = [
  { value: '24h', label: '近 24 小时' },
  { value: '7d', label: '近 7 天' },
  { value: '30d', label: '近 30 天' }
] as const

export type AiWindow = (typeof AI_WINDOW_OPTIONS)[number]['value']

// The four terminal statuses surfaced per (site,route,channel) group. Keys are
// the SummaryRow field names; moderate-text is fail-open, so a non-ok status is
// the audit trail of WHY the call degraded, never a blocked caller.
export const AI_STATUS_META: Record<
  'ok' | 'upstream_error' | 'budget_denied' | 'degraded',
  { label: string; color: AiChipColor }
> = {
  ok: { label: '正常', color: 'success' },
  upstream_error: { label: '上游错误', color: 'danger' },
  budget_denied: { label: '超预算', color: 'warning' },
  degraded: { label: '降级', color: 'default' }
}

// The status columns rendered, in display order.
export const AI_STATUS_KEYS = [
  'ok',
  'upstream_error',
  'budget_denied',
  'degraded'
] as const

// '' channel = the call was served degraded (no upstream dialled).
export const AI_DEGRADED_CHANNEL_LABEL = '(降级 · 未拨上游)'
