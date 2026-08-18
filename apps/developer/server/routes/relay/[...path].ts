export default defineEventHandler(async (event): Promise<unknown> => {
  assertMethod(event, 'GET')
  const raw = event.context.params?.path ?? ''
  const path = new URL(raw, 'http://relay.local/').pathname.slice(1)
  if (!path.startsWith('v1/catalog/')) {
    setResponseStatus(event, 404)
    return { code: 404, message: 'only /v1/catalog/* is relayed' }
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
