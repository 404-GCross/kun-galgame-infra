export default defineNuxtRouteMiddleware(async (to) => {
  const accessToken = useCookie('access_token')

  // Public routes that don't require authentication
  const publicRoutes = ['/auth/login', '/auth/register', '/auth/forgot-password', '/auth/reset-password']

  if (publicRoutes.includes(to.path)) {
    // If already logged in, redirect to dashboard
    if (accessToken.value) {
      return navigateTo('/')
    }
    return
  }

  // If access token exists, proceed
  if (accessToken.value) {
    return
  }

  // No access token — try to refresh using httpOnly cookie
  // (We can't check if refresh_token cookie exists since it's httpOnly)
  const auth = useAuth()
  const refreshed = await auth.refreshAccessToken()
  if (!refreshed) {
    return navigateTo('/auth/login')
  }
})
