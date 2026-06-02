interface ApiOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH'
  body?: Record<string, unknown>
  // URL search params for GET / DELETE. Encoded via `URLSearchParams`
  // so the call site can pass plain objects (`{ client_id: 'X' }`)
  // without manual `encodeURIComponent` / `?key=value` string building.
  // `undefined` values are dropped (matches Nuxt's `$fetch` query
  // convention so call sites can pass optional params conditionally).
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

export const useApi = () => {
  const config = useRuntimeConfig()
  // Dual base: SSR (in-container) uses the docker service URL; browser uses
  // the host-port public URL. apiBaseSsr is empty outside docker → falls back.
  const baseUrl =
    (import.meta.server && config.apiBaseSsr
      ? (config.apiBaseSsr as string)
      : config.public.apiBase) || 'http://127.0.0.1:9277/api/v1'
  const accessToken = useCookie('access_token')

  const getAuthHeaders = (): Record<string, string> => {
    const token = accessToken.value
    return token ? { Authorization: `Bearer ${token}` } : {}
  }

  const handleUnauthorized = async () => {
    // Try to refresh using httpOnly cookie (sent automatically by browser)
    try {
      const response = await $fetch<ApiResponse<{ access_token: string }>>(
        `${baseUrl}/auth/refresh`,
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

    // Clear local token. Then navigate to login, preserving the current
    // URL as ?redirect= so post-login the user lands back where they
    // were — critical for OAuth flow URLs like /oauth/authorize?... that
    // pre-fix were silently dropped on 401, leaving the user stranded
    // on /profile instead of completing the OAuth round-trip.
    //
    // Skip the navigateTo when already on /auth/login (avoids recursive
    // self-redirects when a user back-buttons into a stale tab) or
    // running in SSR (we have no window.location to capture).
    accessToken.value = null
    if (import.meta.client) {
      const here = window.location.pathname + window.location.search
      if (!here.startsWith('/auth/login')) {
        navigateTo(`/auth/login?redirect=${encodeURIComponent(here)}`)
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

    // Build URL with query string. Done here (not via $fetch's `query`
    // option) because we own the full URL path/origin and want the
    // single source of truth for serialization. `undefined` values are
    // skipped so optional params can be passed conditionally.
    let url = `${baseUrl}${endpoint}`
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
    delete: <T>(
      endpoint: string,
      query?: Record<string, string | number | boolean | undefined>
    ) => request<T>(endpoint, { method: 'DELETE', query }),
    patch: <T>(endpoint: string, body?: Record<string, unknown>) =>
      request<T>(endpoint, { method: 'PATCH', body })
  }
}
