export const useAuth = () => {
  const authApi = useAuthApi()
  const userStore = useUserStore()

  const accessToken = useCookie('access_token', {
    maxAge: 60 * 15, // 15 minutes
    sameSite: 'lax'
  })

  const setAccessToken = (token: string) => {
    accessToken.value = token
  }

  const clearAuth = () => {
    accessToken.value = null
    userStore.clearUser()
  }

  const login = async (account: string, password: string) => {
    const response = await authApi.post<LoginResponse>('/auth/login', {
      account,
      password
    })
    if (response.code === 0) {
      setAccessToken(response.data.access_token)
      userStore.setUser(response.data.user)
    }
    return response
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
        setAccessToken(response.data.access_token)
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
    login,
    logout,
    fetchUser,
    refreshAccessToken
  }
}
