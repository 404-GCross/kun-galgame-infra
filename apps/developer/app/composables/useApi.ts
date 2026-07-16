interface ApiOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH'
  body?: Record<string, unknown>
  query?: Record<string, string | number | boolean | undefined>
  headers?: Record<string, string>
}

interface ApiResponse<T> {
  code: number
  message: string
  data: T
}

interface ApiError {
  code: number
  message: string
}

// The portal talks to ONE base on both sides: the same-origin /api/v1 path.
// Nitro's /api/** relay forwards it to the oauth service server-side (see
// server/routes/api/[...path].ts), so there is zero CORS and useFetch derives an
// identical auto-key on server and client (no hydration mismatch).
export const API_BASE = '/api/v1'

export const useApi = () => {
  const accessToken = useCookie('access_token')

  const getAuthHeaders = (): Record<string, string> => {
    const token = accessToken.value
    return token ? { Authorization: `Bearer ${token}` } : {}
  }

  const handleUnauthorized = async () => {
    // Refresh via the SHARED, single-flighted helper so concurrent 401s collapse
    // into ONE /auth/refresh. On success store the new token into THIS
    // composable's cookie ref so the retry below sends it.
    const token = await requestTokenRefresh()
    if (token) {
      accessToken.value = token
      return true
    }

    // Dead session: clear local state and bounce to login, preserving the
    // current URL as ?redirect= so post-login the user lands back here.
    accessToken.value = null
    useUserStore().clearUser()
    if (import.meta.client) {
      const here = window.location.pathname + window.location.search
      if (!here.startsWith('/login')) {
        navigateTo(`/login?redirect=${encodeURIComponent(here)}`)
      }
    }
    return false
  }

  const request = async <T>(
    endpoint: string,
    options: ApiOptions = {},
    retry = true
  ): Promise<ApiResponse<T>> => {
    const { method = 'GET', body, query, headers = {} } = options

    let url = `${API_BASE}${endpoint}`
    if (query) {
      const params = new URLSearchParams()
      for (const [k, v] of Object.entries(query)) {
        if (v !== undefined) params.set(k, String(v))
      }
      const qs = params.toString()
      if (qs) url += `?${qs}`
    }

    try {
      const response = await $fetch<ApiResponse<T>>(url, {
        method,
        body: body ? JSON.stringify(body) : undefined,
        headers: {
          'Content-Type': 'application/json',
          ...getAuthHeaders(),
          ...headers
        },
        credentials: 'include'
      })
      return response
    } catch (error: unknown) {
      const fetchError = error as { statusCode?: number; data?: ApiError }

      if (fetchError.statusCode === 401 && retry) {
        const refreshed = await handleUnauthorized()
        if (refreshed) {
          return request<T>(endpoint, options, false)
        }
      }

      return {
        code: fetchError.data?.code ?? fetchError.statusCode ?? -1,
        message: fetchError.data?.message ?? 'Request failed',
        data: null as T
      }
    }
  }

  return {
    get: <T>(
      endpoint: string,
      query?: Record<string, string | number | boolean | undefined>
    ) => request<T>(endpoint, { method: 'GET', query }),
    post: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'POST', body }),
    patch: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'PATCH', body }),
    delete: <T>(
      endpoint: string,
      query?: Record<string, string | number | boolean | undefined>
    ) => request<T>(endpoint, { method: 'DELETE', query })
  }
}
