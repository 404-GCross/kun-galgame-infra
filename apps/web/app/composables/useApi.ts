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

// Which backend a composable call targets: the OAuth/admin API (default), the
// catalog service, or the Trust & Safety service (each a separate binary on
// its own port). All share the Bearer session and the house envelope.
export type ApiService = 'oauth' | 'catalog' | 'trust'

export const resolveApiBase = (service: ApiService = 'oauth'): string => {
  const config = useRuntimeConfig()
  // Dual base: SSR (in-container) uses the docker service URL; browser uses
  // the host-port public URL. The Ssr entries are empty outside docker →
  // fall back to the public base.
  // Catalog uses ONE base on both sides: the same-origin /catalog-proxy
  // server route. SSR resolves the relative URL through Nitro's internal
  // fetch (no network hop) and the route forwards to catalogApiBaseSsr, so
  // the docker/dev target stays server-side while useFetch derives an
  // IDENTICAL auto-key on server and client — a split base (docker URL on
  // SSR, proxy path in the browser) made the keys differ, so the client
  // couldn't adopt the SSR payload and hydrated from an empty default
  // ("共 0 条候选" hydration mismatch).
  if (service === 'catalog') {
    return (config.public.catalogApiBase as string) || '/catalog-proxy'
  }
  // Trust mirrors catalog: ONE same-origin /trust-proxy base on both sides so
  // useFetch derives an identical auto-key on server and client (no hydration
  // mismatch); the relay forwards to trustApiBaseSsr server-side.
  if (service === 'trust') {
    return (config.public.trustApiBase as string) || '/trust-proxy'
  }
  return (
    (import.meta.server && config.apiBaseSsr
      ? (config.apiBaseSsr as string)
      : (config.public.apiBase as string)) || 'http://127.0.0.1:9277/api/v1'
  )
}

export const useApi = (service: ApiService = 'oauth') => {
  const baseUrl = resolveApiBase(service)
  const accessToken = useCookie('access_token')

  const getAuthHeaders = (): Record<string, string> => {
    const token = accessToken.value
    return token ? { Authorization: `Bearer ${token}` } : {}
  }

  const handleUnauthorized = async () => {
    // Refresh via the SHARED, single-flighted helper so concurrent 401s (a
    // navigation that fires several requests at once) collapse into ONE
    // /auth/refresh instead of racing each other and the backend's token
    // rotation. On success store the new token into THIS composable's cookie
    // ref so the retry below sends it.
    const token = await requestTokenRefresh()
    if (token) {
      accessToken.value = token
      return true
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
    // Also clear the PERSISTED user store. `isLoggedIn` is derived from the
    // store (`!!user`), not the token — so leaving the store set after a
    // dead-session logout keeps `isLoggedIn` stale-true, and pages that trust
    // it ping-pong forever: LoginForm.onMounted auto-redirects to its
    // ?redirect= target, /oauth/authorize auto-consents, both 401 again and
    // bounce back here → infinite redirect (seen when the session row is gone,
    // e.g. after a dev DB reset). Mirrors useAuth.logout()'s clearAuth(); a
    // forced logout should never leave isLoggedIn true.
    useUserStore().clearUser()
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
