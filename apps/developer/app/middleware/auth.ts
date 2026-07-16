export default defineNuxtRouteMiddleware(async (to) => {
  const accessToken = useCookie('access_token')

  // If an access token is present, proceed (validity is re-checked by the
  // layout/page reads, which refresh on 401).
  if (accessToken.value) {
    return
  }

  // No access token. The refresh_token is an httpOnly cookie scoped to
  // /api/v1/auth — the browser never sends it to the Nuxt SSR server on a page
  // navigation (path mismatch), so a SERVER-side refresh always fails.
  // Redirecting here would bounce a returning user with a valid session to
  // /login on every full page load. Defer to the client, where the auth.client
  // plugin restores the session on startup and this middleware refreshes on
  // client-side navigations.
  if (import.meta.server) {
    return
  }

  const auth = useAuth()
  const refreshed = await auth.refreshAccessToken()
  if (!refreshed) {
    const here = to.fullPath
    return navigateTo(`/login?redirect=${encodeURIComponent(here)}`)
  }
})
