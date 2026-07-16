import type { LoginResponse, User } from '~~/shared/types/dev'

// Portal auth: log in with the ecosystem account, restore/refresh the session,
// log out. Registration is NOT here — new accounts are created on the main site
// (the login page links out). Mirrors apps/web's token/refresh conventions: the
// access_token is a short-lived non-httpOnly cookie (JS reads it → Bearer), the
// refresh_token is a backend-managed httpOnly cookie (Path=/api/v1/auth).
export const useAuth = () => {
  const api = useApi()
  const userStore = useUserStore()

  const accessToken = useCookie('access_token', {
    maxAge: 60 * 15, // 15 minutes
    sameSite: 'lax',
    secure: !import.meta.dev
  })

  const setAccessToken = (token: string) => {
    accessToken.value = token
  }

  const clearAuth = () => {
    accessToken.value = null
    userStore.clearUser()
  }

  const login = async (account: string, password: string) => {
    const response = await api.post<LoginResponse>('/auth/login', {
      account,
      password
    })
    if (response.code === 0 && response.data) {
      setAccessToken(response.data.access_token)
      userStore.setUser(response.data.user)
    }
    return response
  }

  const logout = async () => {
    try {
      await api.post('/auth/logout')
    } finally {
      clearAuth()
      navigateTo('/login')
    }
  }

  // Refresh via the shared single-flighted helper. On failure we don't clearAuth
  // — an active session shouldn't be wiped by a transient blip; the caller
  // decides.
  const refreshAccessToken = async () => {
    const token = await requestTokenRefresh()
    if (token) {
      setAccessToken(token)
      return true
    }
    return false
  }

  const fetchUser = async () => {
    if (!accessToken.value) {
      const refreshed = await refreshAccessToken()
      if (!refreshed) return null
    }

    const response = await api.get<User>('/auth/me')
    if (response.code === 0 && response.data) {
      userStore.setUser(response.data)
      return response.data
    }
    return null
  }

  return {
    user: computed(() => userStore.user),
    isLoggedIn: computed(() => userStore.isLoggedIn),
    login,
    logout,
    fetchUser,
    refreshAccessToken
  }
}
