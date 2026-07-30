// GET-only relay for the /explore data browser (namespaced /relay/** — a
// server route at /explore/** would shadow the /explore/work/[id] PAGE route:
// Nitro server routes outrank pages). The browser cannot call api.nextmoe.dev
// directly (no CORS there — it is a server-to-server face), so
// the page sends its requests here and we forward them with the caller's own
// Authorization header (their nm_ key; never stored server-side). Only the two
// public read faces are reachable — anything else 404s.
//
// NOTE (wave 146, 2026-07-30): the /v1/galgame face was delisted and answers
// 410 Gone. Its entry stays whitelisted on purpose — /explore's optional
// cross-face enrichment still requests it and already treats any failure as
// "no data", so the 410 surfaces as a thinner page rather than an error. Drop
// this entry together with those call sites when /explore is reworked onto
// catalog-only data.
export default defineEventHandler(async (event): Promise<unknown> => {
  assertMethod(event, 'GET')
  const raw = event.context.params?.path ?? ''
  // Normalize before the whitelist check: a raw prefix match would pass
  // "v1/catalog/%2e%2e/…" whose dot segments only fold later, inside $fetch's
  // URL resolution, escaping the two allowed faces.
  const path = new URL(raw, 'http://relay.local/').pathname.slice(1)
  if (!path.startsWith('v1/catalog/') && !path.startsWith('v1/galgame/')) {
    setResponseStatus(event, 404)
    return { code: 404, message: 'only /v1/catalog/* and /v1/galgame/* are relayed' }
  }
  const base = useRuntimeConfig(event).nextmoeApiBase
  const query = getQuery(event)
  const auth = getHeader(event, 'authorization')
  try {
    return await $fetch<unknown>(`${base}/${path}` as string, {
      query,
      headers: auth ? { Authorization: auth } : {}
    })
  } catch (e) {
    const err = e as { statusCode?: number; data?: unknown }
    setResponseStatus(event, err.statusCode ?? 502)
    return err.data ?? { code: -1, message: 'upstream unreachable' }
  }
})
