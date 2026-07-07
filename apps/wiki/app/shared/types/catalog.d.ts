// Catalog internal-browser wire types — mirror the Go DTOs served by the
// catalog S2S read face (proxied through the galgame backend). Read-only.

export interface CatalogWorksCell {
  medium_id: number
  claimed: boolean
  status: number
  count: number
}

export interface CatalogKeyCount {
  key: string
  count: number
}

export interface CatalogKindCount {
  kind: number
  count: number
}

export interface CatalogAnchorTierCell {
  source: string
  link_kind: number
  count: number
}

export interface CatalogStatusCount {
  status: number
  count: number
}

export interface CatalogSourceFreshness {
  source: string
  latest_ref?: string
}

export interface CatalogStats {
  works: { total: number; cells: CatalogWorksCell[] }
  entities: {
    persons: number
    credit_names: number
    orphan_credit_names: number
    orgs: number
    labels: number
    characters: number
  }
  credits_by_source: CatalogKeyCount[]
  attributions_by_kind: CatalogKindCount[]
  anchors_by_source_tier: CatalogAnchorTierCell[]
  queues: {
    candidates_by_status: CatalogStatusCount[]
    proposals_by_status: CatalogStatusCount[]
    probable_refs: number
    rejections: number
  }
  llm_bid_verdicts: CatalogKeyCount[]
  source_freshness: CatalogSourceFreshness[]
}

export interface CatalogAnchorRef {
  source: string
  external_id: string
  link_kind: number
  matched_by?: string
}

export interface CatalogWorkDetail {
  work: {
    id: number
    medium_id: number
    display_name: string
    olang: string
    content_rating: number
    status: number
    site?: string
    product_work_id?: number
  }
  titles: { lang: string; title: string; latin?: string; kind: number }[]
  releases: {
    id: number
    kind: number
    released_y?: number
    released_m?: number
    released_d?: number
    anchors: CatalogAnchorRef[]
  }[]
  labels: {
    label_id: number
    display_name: string
    label_kind: number
    kind: number
  }[]
}

export interface CatalogCreditItem {
  credit_name_id: number
  name: string
  lang: string
  latin?: string
  character_id?: number
  character?: string
  note?: string
  source?: string
}

export interface CatalogCredits {
  work_id: number
  groups: {
    role_id: number
    role_key: string
    role_name: string
    credits: CatalogCreditItem[]
  }[]
}

export interface CatalogEntityHit {
  id: string
  entity_type: string
  name: string
  latin?: string
  sources: string[]
  popularity: number
  kind?: number
  person_id?: number
}

export interface CatalogEntitySearch {
  items: CatalogEntityHit[]
  total: number
}

export interface CatalogLabelWorkRow {
  work_id: number
  display_name: string
  medium_id: number
  content_rating: number
  status: number
  kind: number
}

export interface CatalogLabelHead {
  id: number
  display_name: string
  kind: number
}

export interface CatalogLabelWorks {
  label: CatalogLabelHead | null
  total: number
  items: CatalogLabelWorkRow[]
}
