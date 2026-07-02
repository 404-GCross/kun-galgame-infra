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
  code?: number
  message?: string
  // Standard-wire OAuth errors (RFC 6749 §5.2) carry these instead of the
  // envelope's code/message.
  error?: string
  error_description?: string
}

// useAuthApi targets the oauth backend (port 9277, prefix /api/v1).
// Used for login / refresh / me / password / email endpoints.
// Data endpoints (galgame / tag / admin / ...) go through useApi() which targets wiki backend.
export const useAuthApi = () => {
  const config = useRuntimeConfig()
  // Dual base: SSR (in-container) reaches oauth by its compose service name;
  // browser uses the host-port public base. Falls back to public outside docker.
  const baseUrl = (import.meta.server && config.authApiBaseSsr
    ? config.authApiBaseSsr
    : config.public.authApiBase) as string
  const accessToken = useCookie('wiki_access_token')

  const getAuthHeaders = (): Record<string, string> => {
    const token = accessToken.value
    return token ? { Authorization: `Bearer ${token}` } : {}
  }

  const request = async <T>(
    endpoint: string,
    options: ApiOptions = {}
  ): Promise<ApiResponse<T>> => {
    const { method = 'GET', body, headers = {} } = options

    try {
      const response = await $fetch<ApiResponse<T>>(`${baseUrl}${endpoint}`, {
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
      const body = fetchError.data
      return {
        code: body?.code ?? fetchError.statusCode ?? -1,
        // Surface the real reason from either wire shape — the legacy
        // envelope's message or the standard error_description/error.
        message:
          body?.error_description ?? body?.message ?? body?.error ?? 'Request failed',
        data: null as T
      }
    }
  }

  return {
    get: <T>(endpoint: string) => request<T>(endpoint, { method: 'GET' }),
    post: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'POST', body }),
    put: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'PUT', body }),
    delete: <T>(endpoint: string) => request<T>(endpoint, { method: 'DELETE' })
  }
}
