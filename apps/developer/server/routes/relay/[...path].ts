// GET-only relay for the /explore data browser (namespaced /relay/** — a
// server route at /explore/** would shadow the /explore/work/[id] PAGE route:
// Nitro server routes outrank pages). The browser cannot call api.nextmoe.dev
// directly (no CORS there — it is a server-to-server face), so
// the page sends its requests here and we forward them with the caller's own
// Authorization header (their nm_ key; never stored server-side). Only the two
// public read faces are reachable — anything else 404s.
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
