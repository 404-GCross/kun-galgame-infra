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
