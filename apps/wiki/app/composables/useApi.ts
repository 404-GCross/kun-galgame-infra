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

// useApi targets the wiki backend (port 9280, prefix /api).
// For auth operations (login/refresh/etc) use useAuthApi() which targets oauth backend.
export const useApi = () => {
  const config = useRuntimeConfig()
  const baseUrl = config.public.apiBase
  const authApiBase = config.public.authApiBase
  const accessToken = useCookie('wiki_access_token')

  const getAuthHeaders = (): Record<string, string> => {
    const token = accessToken.value
    return token ? { Authorization: `Bearer ${token}` } : {}
  }

  const handleUnauthorized = async () => {
    // SSR-safety: never refresh-or-redirect during server render. The
    // route middleware already gates auth before a page renders; an SSR
    // 401 on an authed endpoint should fail soft (return the error) so
    // the page can still stream, and the client re-attempts + refreshes
    // on hydration via the existing path. Redirecting from inside an
    // SSR data fetch would abort the render unpredictably.
    if (import.meta.server) return false

    // Refresh token lives on oauth backend, not wiki
    try {
      const response = await $fetch<ApiResponse<{ access_token: string }>>(
        `${authApiBase}/auth/refresh`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'include'
        }
      )

      if (response.code === 0) {
        accessToken.value = response.data.access_token
        return true
      }
    } catch {
      // Refresh failed
    }

    accessToken.value = null
    navigateTo('/auth/login')
    return false
  }

  const request = async <T>(
    endpoint: string,
    options: ApiOptions = {},
    retry = true
  ): Promise<ApiResponse<T>> => {
    const { method = 'GET', body, query, headers = {} } = options

    try {
      const response = await $fetch<ApiResponse<T>>(`${baseUrl}${endpoint}`, {
        method,
        query,
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
    put: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'PUT', body }),
    delete: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'DELETE', body }),
    patch: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'PATCH', body })
  }
}
