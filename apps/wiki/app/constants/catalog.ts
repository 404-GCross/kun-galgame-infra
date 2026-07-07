// Catalog internal-browser label maps + static reference data. The browser
// exposes the machinery (graded provenance, gating, orphan doctrine) rather
// than hiding it — these maps render the raw smallint states as legible text.

export const MEDIUM_LABEL: Record<number, string> = { 1: 'galgame', 5: 'ASMR' }

export const WORK_STATUS_LABEL: Record<number, string> = {
  0: 'live',
  1: 'stub',
  2: 'merged'
}

export const CONTENT_RATING_LABEL: Record<number, string> = {
  0: '全年龄',
  1: 'sensitive',
  2: 'R18'
}
export const CONTENT_RATING_COLOR: Record<number, string> = {
  0: 'success',
  1: 'warning',
  2: 'danger'
}

// External-ref link kind = the identity tier (the quality panel's axis).
export const LINK_KIND_LABEL: Record<number, string> = {
  0: 'exact',
  1: 'probable',
  2: 'related'
}
export const LINK_KIND_COLOR: Record<number, string> = {
  0: 'success',
  1: 'warning',
  2: 'default'
}

export const ATTRIBUTION_KIND_LABEL: Record<number, string> = {
  0: 'circle',
  1: 'publisher',
  2: 'developer',
  3: 'brand'
}

export const LABEL_KIND_LABEL: Record<number, string> = {
  0: 'game_brand',
  1: 'bunko',
  2: 'publisher',
  3: 'anime_studio',
  4: 'doujin_circle',
  5: 'group'
}

export const TITLE_KIND_LABEL: Record<number, string> = {
  0: 'official',
  1: 'alias',
  2: 'abbr',
  3: 'search-hint'
}

export const CANDIDATE_STATUS_LABEL: Record<number, string> = {
  0: 'pending',
  1: 'accepted',
  2: 'rejected',
  3: 'deferred'
}

export const PROPOSAL_STATUS_LABEL: Record<number, string> = {
  0: 'open',
  1: 'approved',
  2: 'executed',
  3: 'rejected',
  4: 'withdrawn'
}

export const ENTITY_SEARCH_TYPES = [
  { value: 'names', label: '名义' },
  { value: 'characters', label: '角色' },
  { value: 'labels', label: '厂牌' }
]

// LLM trust map — a static snapshot of the step-12 gold-set calibration
// (2026-07-06). The point is NOT precise recall figures but which layers earn
// trust: deterministic layers are reliable and catch the canary; the LLM is
// only a suggester and can be fooled by smeared aliases, so it never
// auto-decides. Source: refs/proj/12 验收记录.
export const LLM_TRUST_MAP: { layer: string; note: string; tone: string }[] = [
  {
    layer: '字面渲染层',
    note: '简中名↔JP 直接对照，可靠，作为信任基线',
    tone: 'success'
  },
  {
    layer: '确定性 suspect 层',
    note: '别名污染只命中 primary 名 → 判 suspect；金丝雀(atled 挂 CLANNAD bid)被此层拦住',
    tone: 'success'
  },
  {
    layer: '无重叠笔名层',
    note: '弱信号（校准 ~0.478）——保守使用',
    tone: 'warning'
  },
  {
    layer: 'LLM 判定层',
    note: '仅建议、永不自动接受；被涂抹别名可骗到 same@0.95 → 人审兜底',
    tone: 'danger'
  }
]

// The apps/web admin catalog area (07 三桶) — where all decisions happen. The
// browser only links here; it never mutates. Derived from the oauth origin
// (apps/web is the oauth frontend). Non-deep-linked on purpose (apps/web owns
// its routes); this is "go handle it in admin".
export const useCatalogAdminUrl = () => {
  const config = useRuntimeConfig()
  const base = (config.public.authApiBase as string) || ''
  try {
    return new URL(base).origin + '/admin/catalog'
  } catch {
    return '/'
  }
}
