interface ApiOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH'
  body?: Record<string, unknown>
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

export const useApi = () => {
  const config = useRuntimeConfig()
  const baseUrl = config.public.apiBase || 'http://localhost:9277/api/v1'

  const getAuthHeaders = (): Record<string, string> => {
    const token = useCookie('access_token').value
    return token ? { Authorization: `Bearer ${token}` } : {}
  }

  const handleUnauthorized = async () => {
    const refreshToken = useCookie('refresh_token')
    const accessToken = useCookie('access_token')

    if (refreshToken.value) {
      // Try to refresh the token
      try {
        const response = await $fetch<ApiResponse<{ access_token: string; refresh_token: string }>>(
          `${baseUrl}/auth/refresh`,
          {
            method: 'POST',
            body: JSON.stringify({ refresh_token: refreshToken.value }),
            headers: { 'Content-Type': 'application/json' },
          }
        )

        if (response.code === 0) {
          accessToken.value = response.data.access_token
          refreshToken.value = response.data.refresh_token
          return true
        }
      } catch {
        // Refresh failed
      }
    }

    // Clear tokens and redirect to login
    accessToken.value = null
    refreshToken.value = null
    navigateTo('/auth/login')
    return false
  }

  const request = async <T>(
    endpoint: string,
    options: ApiOptions = {},
    retry = true
  ): Promise<ApiResponse<T>> => {
    const { method = 'GET', body, headers = {} } = options

    try {
      const response = await $fetch<ApiResponse<T>>(`${baseUrl}${endpoint}`, {
        method,
        body: body ? JSON.stringify(body) : undefined,
        headers: {
          'Content-Type': 'application/json',
          ...getAuthHeaders(),
          ...headers,
        },
      })

      return response
    } catch (error: unknown) {
      // Handle fetch errors
      const fetchError = error as { statusCode?: number; data?: ApiError }

      if (fetchError.statusCode === 401 && retry) {
        // Try to refresh token and retry request
        const refreshed = await handleUnauthorized()
        if (refreshed) {
          return request<T>(endpoint, options, false)
        }
      }

      // Return error response
      return {
        code: fetchError.data?.code ?? fetchError.statusCode ?? -1,
        message: fetchError.data?.message ?? 'Request failed',
        data: null as T,
      }
    }
  }

  return {
    get: <T>(endpoint: string) => request<T>(endpoint, { method: 'GET' }),
    post: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'POST', body }),
    put: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'PUT', body }),
    delete: <T>(endpoint: string) => request<T>(endpoint, { method: 'DELETE' }),
    patch: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'PATCH', body }),
  }
}
