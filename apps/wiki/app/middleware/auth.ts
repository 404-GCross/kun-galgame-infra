export default defineNuxtRouteMiddleware(async (to) => {
  const accessToken = useCookie('access_token')

  const publicRoutes = ['/auth/login']

  if (publicRoutes.includes(to.path)) {
    if (accessToken.value && !to.query.redirect) {
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
