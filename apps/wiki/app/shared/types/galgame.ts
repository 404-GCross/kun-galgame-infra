export interface Galgame {
  id: number
  vndb_id: string
  bid?: number
  released: string
  name_en_us: string
  name_ja_jp: string
  name_zh_cn: string
  name_zh_tw: string
  banner: string
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
  }
}
