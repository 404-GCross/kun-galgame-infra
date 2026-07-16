// Same-origin relay for the oauth REST API. The browser only ever talks to
// developer.nextmoe.dev; every /api/** call is forwarded here to the oauth
// service SERVER-side, so there is ZERO CORS and no backend change (design §9 /
// 06b 裁定 2). Method, body, query, Cookie and Authorization headers pass
// through untouched, and oauth's Set-Cookie (the httpOnly refresh_token, host-
// only + Path=/api/v1/auth) is relayed back so it scopes to THIS origin.
//
// The oauth base is a SERVER-only runtime var (NUXT_OAUTH_API_BASE) — never
// exposed to the browser. The path is preserved: /api/v1/auth/login →
// <base>/api/v1/auth/login.
export default defineEventHandler((event) => {
  const path = event.context.params?.path ?? ''
  const segments = path.split('/')
  if (segments.some((s) => s === '..')) {
    throw createError({ statusCode: 404, statusMessage: 'Not Found' })
  }
  const base = useRuntimeConfig().oauthApiBase as string
  const search = getRequestURL(event).search
  return proxyRequest(event, `${base}/api/${path}${search}`)
})
