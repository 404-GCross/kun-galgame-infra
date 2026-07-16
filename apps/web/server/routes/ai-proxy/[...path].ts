// Same-origin relay for the AI-gateway ADMIN api. The AI service has no public
// domain by design (it lives on the docker network only), so the browser cannot
// reach it directly — admin UI calls go to this route on the web app's own
// origin and are forwarded server-side. The Bearer session header passes
// through untouched; auth/roles (ai.usage_view) are enforced by the AI service
// itself. Only the admin/ subtree is relayed — the S2S surface (moderate-text)
// is for backend clients and stays unexposed.
export default defineEventHandler((event) => {
  const path = event.context.params?.path ?? ''
  const segments = path.split('/')
  if (segments[0] !== 'admin' || segments.some((s) => s === '' || s === '..')) {
    throw createError({ statusCode: 404, statusMessage: 'Not Found' })
  }
  const config = useRuntimeConfig()
  const base = (config.aiApiBaseSsr as string) || 'http://127.0.0.1:9284/api/v1'
  const search = getRequestURL(event).search
  return proxyRequest(event, `${base}/${path}${search}`)
})
