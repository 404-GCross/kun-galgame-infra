// Galgame wire types — GENERATED from the galgame-wiki OpenAPI spec.
//
// These are aliases of the code-first contract (docs/galgame_wiki/read-openapi.yaml
// → app/shared/types/generated/galgame-read-api.ts, via `pnpm gen:types:galgame-read`).
// Backend field changes now surface here at compile time instead of failing at
// runtime. Only the app-local shapes at the bottom (admin stats, Pagination) are
// hand-written — they have no server contract.
import type { components } from './generated/galgame-read-api'

type Schemas = components['schemas']

// Full galgame graph (GET /galgame/:gid; also GET /galgame list items, which
// populate a lighter preload subset — the extra relations are optional).
export type Galgame = Schemas['GalgameDetail']

export type GalgameCover = Schemas['CalendarCover']
export type GalgameScreenshot = Schemas['DetailScreenshot']
export type GalgameAlias = Schemas['DetailAlias']
export type GalgameTag = Schemas['DetailTag']
export type GalgameTagRelation = Schemas['DetailTagRelation']
export type GalgameOfficial = Schemas['DetailOfficial']
export type GalgameOfficialRelation = Schemas['DetailOfficialRelation']
export type GalgameEngine = Schemas['DetailEngine']
export type GalgameSeries = Schemas['DetailSeries']
export type GalgameLink = Schemas['DetailLink']

// Revision / PR: snapshot is the structured Snapshot object (was an opaque
// string/object hand-type). Note the revision carries `note` (not `message`).
export type GalgameRevision = Schemas['RevisionResponse']
export type GalgamePR = Schemas['PRResponse']
export type Snapshot = Schemas['Snapshot']

export type ContributorWithUser = Schemas['GalgameContributorWithUser']

// ── App-local shapes (no server contract) ──────────────────────────────

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
