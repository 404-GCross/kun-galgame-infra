export default defineNuxtRouteMiddleware(async (to) => {
  const accessToken = useCookie('access_token')

  const publicRoutes = ['/auth/login', '/auth/callback']

  if (publicRoutes.includes(to.path)) {
    // /auth/callback always processes the OAuth response, even if we already
    // have a token — otherwise a leftover stale token would silently swallow
    // the new login attempt.
    if (to.path === '/auth/callback') return

    if (accessToken.value) {
      const auth = useAuth()
      if (!auth.user.value) {
        await auth.fetchUser()
      }
      return navigateTo('/')
    }
    return
  }

  if (accessToken.value) {
    return
  }

  const auth = useAuth()
  const refreshed = await auth.refreshAccessToken()
  if (!refreshed) {
    return navigateTo('/auth/login')
  }
})
