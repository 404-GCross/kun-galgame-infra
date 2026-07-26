import type { LoginResponse, User } from '~~/shared/types/dev'

// Portal auth: log in with the ecosystem account, restore/refresh the session,
// log out. Registration is NOT here — new accounts are created on the main site
// (the login page links out). Mirrors apps/web's token/refresh conventions: the
// access_token is a short-lived non-httpOnly cookie (JS reads it → Bearer), the
// refresh_token is a backend-managed httpOnly cookie (Path=/api/v1/auth).
export const useAuth = () => {
  const api = useApi()
  const userStore = useUserStore()
  const refreshTransient = useRefreshTransient()

  const accessToken = useCookie('access_token', {
    maxAge: 60 * 15, // 15 minutes
    sameSite: 'lax',
    secure: !import.meta.dev
  })

  // Which login produced this session — 'oauth' (SSO) or 'password' (fallback).
  // Selects the refresh/logout path (OAuth tokens can't use /api/v1/auth/*).
  // For SSO it is written server-side by the exchange route; for the password
  // fallback we write it here. Both live at Path=/ so the client can read it.
  const authMode = useCookie('auth_mode', {
    maxAge: 60 * 60 * 24 * 90,
    sameSite: 'lax',
    secure: !import.meta.dev
  })

  const setAccessToken = (token: string) => {
    accessToken.value = token
    // A token landing means the refresh path is healthy again — retire any
    // outstanding transient-failure banner.
    refreshTransient.value = false
  }

  const clearAuth = () => {
    accessToken.value = null
    authMode.value = null
    userStore.clearUser()
    refreshTransient.value = false // a stale banner must not outlive the session
  }

  const login = async (account: string, password: string) => {
    const response = await api.post<LoginResponse>('/auth/login', {
      account,
      password
    })
    if (response.code === 0 && response.data) {
      setAccessToken(response.data.access_token)
      userStore.setUser(response.data.user)
      authMode.value = 'password'
    }
    return response
  }

  const logout = async () => {
    // Tear down BOTH session modes, not just the current auth_mode: password
    // and SSO logins write same-named refresh_token cookies on different paths
    // (/api/v1/auth vs /auth), so a surviving other-mode cookie would silently
    // log the user back in on the next guarded navigation. Each call is a
    // harmless no-op when that mode has no session.
    await Promise.allSettled([
      api.post('/auth/logout'),
      $fetch('/auth/logout', { method: 'POST', credentials: 'include' })
    ])
    clearAuth()
    navigateTo('/')
  }

  // Refresh via the shared single-flighted helper. On failure we don't clearAuth
  // — an active session shouldn't be wiped by a transient blip; the caller
  // decides.
  const refreshAccessToken = async () => {
    const token = await requestTokenRefresh()
    if (typeof token === 'string') {
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
    setAccessToken,
    login,
    logout,
    fetchUser,
    refreshAccessToken
  }
}
