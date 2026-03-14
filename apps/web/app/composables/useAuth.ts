export interface User {
  uuid: string
  name: string
  email: string
  avatar: string
  bio: string
  moemoepoint: number
  status: number
  created_at: string
}

export interface TokenPair {
  access_token: string
  refresh_token: string
}

export interface LoginResponse {
  user: User
  tokens: TokenPair
}

export const useAuth = () => {
  const api = useApi()
  const user = useState<User | null>('user', () => null)
  const isLoggedIn = computed(() => !!user.value)

  const accessToken = useCookie('access_token', {
    maxAge: 60 * 15, // 15 minutes
    sameSite: 'lax',
  })

  const refreshToken = useCookie('refresh_token', {
    maxAge: 60 * 60 * 24 * 7, // 7 days
    sameSite: 'lax',
  })

  const setTokens = (tokens: TokenPair) => {
    accessToken.value = tokens.access_token
    refreshToken.value = tokens.refresh_token
  }

  const clearTokens = () => {
    accessToken.value = null
    refreshToken.value = null
  }

  const login = async (email: string, password: string) => {
    const response = await api.post<LoginResponse>('/auth/login', {
      email,
      password,
    })
    if (response.code === 0) {
      setTokens(response.data.tokens)
      user.value = response.data.user
    }
    return response
  }

  const register = async (name: string, email: string, password: string) => {
    const response = await api.post<User>('/auth/register', {
      name,
      email,
      password,
    })
    return response
  }

  const logout = async () => {
    try {
      await api.post('/auth/logout')
    } finally {
      clearTokens()
      user.value = null
      navigateTo('/auth/login')
    }
  }

  const refreshAccessToken = async () => {
    if (!refreshToken.value) return false

    try {
      const response = await api.post<TokenPair>('/auth/refresh', {
        refresh_token: refreshToken.value,
      })
      if (response.code === 0) {
        setTokens(response.data)
        return true
      }
    } catch {
      clearTokens()
      user.value = null
    }
    return false
  }

  const fetchUser = async () => {
    if (!accessToken.value) return null

    try {
      const response = await api.get<User>('/auth/me')
      if (response.code === 0) {
        user.value = response.data
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

  return {
    user,
    isLoggedIn,
    login,
    register,
    logout,
    fetchUser,
    refreshAccessToken,
    forgotPassword,
    resetPassword,
    changePassword,
  }
}
