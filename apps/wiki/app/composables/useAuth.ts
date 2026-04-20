// Session management for the wiki admin.
// Login itself is handled by useOAuthLogin() (Authorization Code + PKCE);
// this composable just reads/clears the session and keeps the user store
// hydrated so the layout / middleware can gate routes.
export const useAuth = () => {
  const authApi = useAuthApi()
  const userStore = useUserStore()

  const accessToken = useCookie('access_token', {
    maxAge: 60 * 15,
    sameSite: 'lax'
  })

  const clearAuth = () => {
    accessToken.value = null
    userStore.clearUser()
  }

  const logout = async () => {
    try {
      await authApi.post('/auth/logout')
    } finally {
      clearAuth()
      navigateTo('/auth/login')
    }
  }

  const refreshAccessToken = async () => {
    try {
      const response = await authApi.post<RefreshResponse>('/auth/refresh')
      if (response.code === 0) {
        accessToken.value = response.data.access_token
        return true
      }
    } catch {
      clearAuth()
    }
    return false
  }

  const fetchUser = async () => {
    if (!accessToken.value) {
      const refreshed = await refreshAccessToken()
      if (!refreshed) return null
    }

    const response = await authApi.get<User>('/auth/me')
    if (response.code === 0) {
      userStore.setUser(response.data)
      return response.data
    }
    return null
  }

  return {
    user: computed(() => userStore.user),
    isLoggedIn: computed(() => userStore.isLoggedIn),
    isAdmin: computed(() => userStore.isAdmin),
    logout,
    fetchUser,
    refreshAccessToken
  }
}
