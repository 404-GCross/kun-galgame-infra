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
  // Dual base: SSR (in-container) uses docker service URLs; browser uses the
  // host-port public URLs. *Ssr is empty outside docker → falls back to public.
  const baseUrl = (import.meta.server && config.apiBaseSsr
    ? config.apiBaseSsr
    : config.public.apiBase) as string
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

    // Refresh through the SHARED, single-flighted OAuth client refresh
    // (grant_type=refresh_token via the wiki_refresh_token cookie). This is the
    // wiki's CORRECT refresh path — NOT /auth/refresh, which targets the IdP's
    // separate first-party session and only worked on localhost (same-site) by
    // borrowing it, failing cross-site in prod. Single-flight collapses
    // concurrent 401s into one refresh. Store the new token into THIS
    // composable's cookie ref so the retry below sends it.
    const token = await requestTokenRefresh()
    if (token) {
      accessToken.value = token
      return true
    }

    // Definitive failure: clear the full session (tokens + persisted user store)
    // so `isLoggedIn` can't stay stale-true (→ login/redirect loops), then go to
    // login. clearAuth lives in useAuth (single source of clearing logic).
    useAuth().clearAuth()
    navigateTo('/auth/login')
    return false
  }

  // Wiki backend returns business-level auth errors as HTTP 200 +
  // {code: 10001|10003, message, data: null}, NOT as HTTP 401. That
  // means $fetch resolves normally (no throw) and the only signal we
  // have is the envelope's `code`. Treat these the same as a thrown
  // 401: try one refresh + retry, then bubble null with the error
  // message so callers can fall back gracefully (e.g. List.vue's
  // `r.code === 0 ? r.data : null` path).
  //
  // Codes:
  //   10001 — "未授权，请先登录" (no / missing token)
  //   10003 — "令牌已过期，请重新登录" (token expired)
  const AUTH_ERROR_CODES = new Set([10001, 10003])

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

      // Business-level auth error in 200-envelope (see comment above).
      if (AUTH_ERROR_CODES.has(response.code) && retry) {
        const refreshed = await handleUnauthorized()
        if (refreshed) {
          return request<T>(endpoint, options, false)
        }
      }

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
