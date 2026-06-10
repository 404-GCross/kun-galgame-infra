export interface Galgame {
  id: number
  vndb_id: string
  bid?: number
  // Release date is "YYYY-MM-DD" or null (= unknown). release_date_tba
  // marks "announced, exact date pending" and is independent — a
  // scheduled game can have both (e.g. date='2026-06-01', tba=true
  // meaning "approximate target"). Use formatReleaseDate() for display.
  release_date: string | null
  release_date_tba: boolean
  name_en_us: string
  name_ja_jp: string
  name_zh_cn: string
  name_zh_tw: string
  banner: string                       // legacy URL, permanent fallback when no cover exists
  // effective_banner_hash is the backend-derived "currently shown" banner:
  // the pinned cover's image_hash (sort_order=0) if any, else nil.
  // PR5 retired the legacy banner_image_hash column; galgame_cover is
  // now the sole image_service reference. Prefer resolveBannerUrl over
  // reading this directly.
  effective_banner_hash?: string | null
  intro_en_us: string
  intro_ja_jp: string
  intro_zh_cn: string
  intro_zh_tw: string
  content_limit: string
  status: number // 0 published, 1 banned, 2 draft
  view: number
  original_language: string
  age_limit: string
  user_id: number
  series_id?: number
  created: string
  updated: string
  alias?: GalgameAlias[]
  tag?: GalgameTagRelation[]
  official?: GalgameOfficialRelation[]
  series?: GalgameSeries | null
  cover?: GalgameCover[]
  screenshot?: GalgameScreenshot[]
}

// GalgameCover — one entry in a galgame's cover candidate set.
// `image_hash` refers to image_service (use resolveBannerUrl /
// imageHashUrl to build URLs). `sort_order=0` is the pinned banner (at
// most one per galgame, enforced by DB-level partial unique index).
// `sexual` / `violence` are 0..3 ratings; v1 frontend may read but the
// gating decision still flows through content_limit + user settings.
export interface GalgameCover {
  galgame_id: number
  image_hash: string
  sort_order: number
  sexual: number
  violence: number
  source: string
  source_key: string
  created: string
}

// GalgameScreenshot — gallery / CG entry. Same shape as GalgameCover
// plus `caption`. No "pinned" uniqueness — sort_order is just display order.
export interface GalgameScreenshot {
  galgame_id: number
  image_hash: string
  sort_order: number
  caption: string
  sexual: number
  violence: number
  source: string
  source_key: string
  created: string
}

export interface GalgameAlias {
  id: number
  name: string
  galgame_id: number
}

export interface GalgameTag {
  id: number
  name: string
  category: string
  description?: string
  galgame_count: number
}

export interface GalgameTagRelation {
  galgame_id: number
  tag_id: number
  spoiler_level: number
  tag?: GalgameTag
}

export interface GalgameOfficial {
  id: number
  name: string
  original: string
  link: string
  category: string
  lang: string
  description?: string
  galgame_count: number
}

export interface GalgameOfficialRelation {
  galgame_id: number
  official_id: number
  official?: GalgameOfficial
}

export interface GalgameEngine {
  id: number
  name: string
  description?: string
  galgame_count: number
}

export interface GalgameSeries {
  id: number
  name: string
  description: string
  galgame_count: number
}

export interface AdminStatsTotals {
  galgame_tag: number
  galgame_official: number
  galgame_engine: number
  galgame_series: number
  galgame_link: number
  galgame_pr: number
  galgame_revision: number
}

export interface AdminStatsDaily {
  date: string
  galgame_tag: number
  galgame_official: number
  galgame_engine: number
  galgame_series: number
  galgame_link: number
  galgame_pr: number
  galgame_revision: number
}

export interface AdminStatsResponse {
  totals: AdminStatsTotals
  daily: AdminStatsDaily[]
}

export interface Pagination<T> {
  data: T[]
  total: number
  page: number
  limit: number
}

export interface GalgameLink {
  id: number
  name: string
  link: string
  galgame_id: number
  user_id: number
  created: string
  updated: string
}

export interface GalgameRevision {
  id: number
  galgame_id: number
  revision: number
  snapshot: string | object
  message: string
  is_minor: boolean
  user_id: number
  created: string
}

export interface GalgamePR {
  id: number
  galgame_id: number
  user_id: number
  status: number // 0 pending, 1 merged, 2 declined
  title: string
  message: string
  base_revision: number
  snapshot: string | object
  created: string
  updated: string
}

export interface ContributorWithUser {
  id: number
  galgame_id: number
  user_id: number
  created: string
  user?: {
    id: number
    name: string
    avatar: string
    avatar_image_hash?: string | null
  }
}
