export interface CatalogStatsMedium {
  medium_id: number
  medium: string
  count: number
}

export interface CatalogStats {
  works: {
    total: number
    by_medium: CatalogStatsMedium[]
  }
  entities: {
    labels: number
    characters: number
    credit_names: number
    persons: number
  }
}
