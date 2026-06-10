export const useAuth = () => {
  const api = useApi()
  const userStore = useUserStore()

  const accessToken = useCookie('access_token', {
    maxAge: 60 * 15, // 15 minutes
    sameSite: 'lax',
    secure: !import.meta.dev
  })

  // Note: refresh_token is managed by the backend as an httpOnly cookie.
  // We cannot read it from JS, which is the point — it's secure from XSS.

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
      password,
    })
    if (response.code === 0) {
      setAccessToken(response.data.access_token)
      userStore.setUser(response.data.user)
    }
    return response
  }

  // Two-step registration — see docs/integration/oauth/05-registration.md.
  //
  // 1) sendRegisterCode: pre-checks name/email uniqueness on the server
  //    side, generates a 6-digit code, emails it to the prospective
  //    address. Returns `{ code: 0, message }` on success, business
  //    errors on conflict / rate-limit.
  // 2) register: submits name + email + password + the 6-digit code.
  //    On success the backend issues tokens + sets refresh cookie and
  //    we drop straight into the logged-in state (auto-login) so the
  //    unified-registration redirect chain can immediately continue
  //    into /oauth/authorize.
  const sendRegisterCode = async (name: string, email: string) => {
    return api.post('/auth/register/send-code', { name, email })
  }

  const register = async (
    name: string,
    email: string,
    password: string,
    code: string
  ) => {
    const response = await api.post<LoginResponse>('/auth/register', {
      name,
      email,
      password,
      code,
    })
    if (response.code === 0) {
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
      navigateTo('/auth/login')
    }
  }

  // Probe-style call: returns true/false without side effects so callers
  // (Container.vue's /oauth/authorize check, middleware/auth.ts, etc.)
  // can branch on session liveness. We deliberately bypass `useApi` here
  // — its 401 handler auto-navigateTo's /auth/login without a redirect
  // param, which would clobber the caller's own state-management (e.g.
  // Container's needsLogin prompt) and lose the OAuth flow URL.
  //
  // The `credentials: 'include'` lets the browser send the
  // refresh_token httpOnly cookie scoped to /api/v1/auth.
  const refreshAccessToken = async () => {
    const config = useRuntimeConfig()
    const baseUrl = config.public.apiBase || 'http://127.0.0.1:9277/api/v1'
    try {
      const response = await $fetch<{ code: number; data?: { access_token: string } }>(
        `${baseUrl}/auth/refresh`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'include'
        }
      )
      if (response.code === 0 && response.data) {
        setAccessToken(response.data.access_token)
        return true
      }
    } catch {
      // Refresh failed (no cookie / expired / network) — caller decides
      // what to do. Don't clearAuth here; absent a stored token there's
      // nothing to clear, and an active session shouldn't be wiped by a
      // transient network blip.
    }
    return false
  }

  const fetchUser = async () => {
    if (!accessToken.value) {
      // No access token — try refreshing (httpOnly cookie may still be valid)
      const refreshed = await refreshAccessToken()
      if (!refreshed) return null
    }

    try {
      const response = await api.get<User>('/auth/me')
      if (response.code === 0) {
        userStore.setUser(response.data)
        return response.data
      }
    } catch {
      // Try to refresh token
      const refreshed = await refreshAccessToken()
      if (refreshed) {
        return fetchUser()
      }
    }
    return null
  }

  const forgotPassword = async (email: string) => {
    return api.post('/auth/password/forgot', { email })
  }

  const resetPassword = async (token: string, password: string) => {
    return api.post('/auth/password/reset', { token, password })
  }

  const changePassword = async (oldPassword: string, newPassword: string) => {
    return api.put('/auth/password', {
      old_password: oldPassword,
      new_password: newPassword,
    })
  }

  const sendEmailChangeCode = async (newEmail: string) => {
    return api.post('/auth/email/send-code', { new_email: newEmail })
  }

  const changeEmail = async (code: string, newEmail: string) => {
    return api.put('/auth/email', { code, new_email: newEmail })
  }

  // PATCH /auth/me — partial self-service profile update. Pointer/optional
  // semantics on the server: only the keys present in `payload` change;
  // empty string clears the field. On success the response IS the fresh
  // UserResponse, so we push it straight into the store (no extra /auth/me
  // round-trip). Email/password are NOT here — they have their own
  // verified flows (changeEmail / changePassword).
  const updateProfile = async (payload: {
    name?: string
    bio?: string
    avatar?: string
    avatar_image_hash?: string
  }) => {
    const response = await api.patch<User>('/auth/me', payload)
    if (response.code === 0 && response.data) {
      userStore.setUser(response.data)
    }
    return response
  }

  return {
    user: computed(() => userStore.user),
    isLoggedIn: computed(() => userStore.isLoggedIn),
    isAdmin: computed(() => userStore.isAdmin),
    isRen: computed(() => userStore.isRen),
    login,
    sendRegisterCode,
    register,
    logout,
    fetchUser,
    refreshAccessToken,
    forgotPassword,
    resetPassword,
    changePassword,
    sendEmailChangeCode,
    changeEmail,
    updateProfile,
  }
}
