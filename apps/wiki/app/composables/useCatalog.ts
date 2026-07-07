// useCatalog wraps the galgame backend's staff-only catalog proxy
// (/galgame/catalog/*). The Basic S2S credentials live in the galgame backend;
// the browser sends only its Bearer token (useApi attaches it), which the
// backend gates to admin/moderator. Read-only.
import type {
  CatalogStats,
  CatalogWorkDetail,
  CatalogCredits,
  CatalogEntitySearch,
  CatalogLabelWorks
} from '~/shared/types/catalog'

export const useCatalog = () => {
  const api = useApi()

  return {
    stats: () => api.get<CatalogStats>('/galgame/catalog/stats'),
    work: (id: number | string) =>
      api.get<CatalogWorkDetail>(`/galgame/catalog/works/${id}`),
    credits: (id: number | string) =>
      api.get<CatalogCredits>(`/galgame/catalog/works/${id}/credits`),
    search: (q: string, type: string, locale?: string, limit = 20) =>
      api.get<CatalogEntitySearch>('/galgame/catalog/search/entities', {
        q,
        type,
        locale,
        limit
      }),
    labelWorks: (id: number | string, limit = 50, offset = 0) =>
      api.get<CatalogLabelWorks>(`/galgame/catalog/labels/${id}/works`, {
        limit,
        offset
      })
  }
}
