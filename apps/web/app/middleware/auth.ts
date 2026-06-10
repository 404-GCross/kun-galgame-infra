export default defineNuxtRouteMiddleware(async (to) => {
  const accessToken = useCookie('access_token')

  // Public routes that don't require authentication
  const publicRoutes = ['/auth/login', '/auth/register', '/auth/forgot-password', '/auth/reset-password', '/oauth/authorize']

  if (publicRoutes.includes(to.path)) {
    // If already logged in and no redirect param, go to dashboard/profile
    if (accessToken.value && !to.query.redirect) {
      const auth = useAuth()
      if (!auth.user.value) {
        await auth.fetchUser()
      }
      return navigateTo(auth.isAdmin.value ? '/' : '/profile')
    }
    return
  }

  // If access token exists, proceed
  if (accessToken.value) {
    return
  }

  // No access token. The refresh_token is an httpOnly cookie scoped to
  // /api/v1/auth — the browser never sends it to the Nuxt SSR server (path
  // mismatch) and $fetch can't forward it, so a SERVER-side refresh always
  // fails. Redirecting here would bounce a returning user with a perfectly
  // valid session to /auth/login on every full page load (the "refresh the
  // page to log in" bug). Defer to the client, where the browser DOES send the
  // refresh cookie: the auth.client plugin restores the session on startup, and
  // this middleware refreshes on client-side navigations. A genuinely expired
  // session is caught by the layout's mount guard.
  if (import.meta.server) {
    return
  }

  const auth = useAuth()
  const refreshed = await auth.refreshAccessToken()
  if (!refreshed) {
    return navigateTo('/auth/login')
  }
})
