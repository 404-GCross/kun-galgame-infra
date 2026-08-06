// Trust & Safety review-queue vocabulary: display labels + semantic colors for
// the wire constants of the trust admin API (docs/trust/admin-openapi.yaml).
// The numeric values mirror
// apps/api/internal/platform/trust/model/constants.go and are persisted —
// never renumber. `-1` is the API's "no filter" sentinel for optional query
// params.

export const TRUST_FILTER_ALL = -1

// Semantic chip colors (mirrors @kungal/ui-core's KunUIColor union — the
// project palette only, never Tailwind built-ins).
export type TrustChipColor =
  | 'default'
  | 'primary'
  | 'secondary'
  | 'success'
  | 'warning'
  | 'danger'
  | 'info'

// review_item.status: 0=pending 1=claimed 2=actioned 3=dismissed
export const REVIEW_STATUS = {
  pending: 0,
  claimed: 1,
  actioned: 2,
  dismissed: 3
} as const

export const REVIEW_STATUS_LABELS: Record<number, string> = {
  0: '待处理',
  1: '已认领',
  2: '已处置',
  3: '已驳回'
}

export const REVIEW_STATUS_COLORS: Record<number, TrustChipColor> = {
  0: 'warning',
  1: 'info',
  2: 'success',
  3: 'default'
}

export const REVIEW_SOURCE = {
  reports: 0,
  aiText: 1,
  aiImage: 2,
  communityForward: 3,
  mislabel: 4,
  manual: 5,
  aiSample: 6
} as const

// review_item.source: 0=reports 1=ai_text 2=ai_image 3=community_forward
// 4=mislabel 5=manual 6=ai_sample
export const REVIEW_SOURCE_LABELS: Record<number, string> = {
  0: '举报',
  1: 'AI 文本',
  2: 'AI 图像',
  3: '社区转交',
  4: '错误标注',
  5: '人工',
  // A clean verdict drawn at random so a human can confirm the classifier was
  // right to clear it — the only source that measures what the pipeline MISSES.
  // Never enforced: nothing is hidden by a calibration draw.
  6: '抽检'
}

// disposition.action: 0=none 1=hide 2=remove 3=warn_user 4=restrict
// 5=escalate_idp
export const ACTION_LABELS: Record<number, string> = {
  0: '无操作(复核放行)',
  1: '隐藏',
  2: '移除',
  3: '警告用户',
  4: '限制',
  5: '升级至账号中心'
}

// The concrete actions an `actioned` decision may carry (0=none is not a real
// enforcement action, so it is never offered).
export const DECIDE_ACTIONS: ReadonlyArray<number> = [1, 2, 3, 4, 5]

// report.status: 0=received 1=linked 2=folded
export const REPORT_STATUS_LABELS: Record<number, string> = {
  0: '已接收',
  1: '已关联',
  2: '已折叠'
}

export const REPORT_STATUS_COLORS: Record<number, TrustChipColor> = {
  0: 'warning',
  1: 'info',
  2: 'default'
}

// disposition.callback_status: 0=pending 1=delivered 2=dead_letter
export const CALLBACK_STATUS = {
  pending: 0,
  delivered: 1,
  deadLetter: 2
} as const

export const CALLBACK_STATUS_LABELS: Record<number, string> = {
  0: '待投递',
  1: '已投递',
  2: '死信'
}

export const CALLBACK_STATUS_COLORS: Record<number, TrustChipColor> = {
  0: 'warning',
  1: 'success',
  2: 'danger'
}

// trust_term.kind: 0=suspect (hold — enqueue, don't block) 1=banned (deny — the
// synchronous check rejects the write). Mirrors
// apps/api/internal/platform/trust/model/constants.go (persisted — never renumber).
export const TERM_KIND = {
  suspect: 0,
  banned: 1
} as const

export const TERM_KIND_LABELS: Record<number, string> = {
  0: '疑似(入队复核)',
  1: '禁用(直接拦截)'
}

export const TERM_KIND_COLORS: Record<number, TrustChipColor> = {
  0: 'warning',
  1: 'danger'
}
