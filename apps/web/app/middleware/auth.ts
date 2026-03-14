export default defineNuxtRouteMiddleware(async (to) => {
  const accessToken = useCookie('access_token')
  const refreshToken = useCookie('refresh_token')

  // Public routes that don't require authentication
  const publicRoutes = ['/auth/login', '/auth/register', '/auth/forgot-password', '/auth/reset-password']

  if (publicRoutes.includes(to.path)) {
    // If already logged in, redirect to dashboard
    if (accessToken.value) {
      return navigateTo('/')
    }
    return
  }

  // Check if user has tokens
  if (!accessToken.value && !refreshToken.value) {
    return navigateTo('/auth/login')
  }

  // If access token expired but refresh token exists, try to refresh
  if (!accessToken.value && refreshToken.value) {
    const auth = useAuth()
    const refreshed = await auth.refreshAccessToken()
    if (!refreshed) {
      return navigateTo('/auth/login')
    }
  }
})
