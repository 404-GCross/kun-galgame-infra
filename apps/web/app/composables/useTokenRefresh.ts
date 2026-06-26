// Single-flighted access-token refresh, shared by EVERY refresh path: the route
// middleware, the boot plugin, useAuth.refreshAccessToken, and useApi's 401
// retry. Returns the new access_token (string) on success, or null. Each caller
// stores the result into its own `access_token` cookie ref.
//
// WHY single-flight: when the 15-min access_token expires, a single navigation
// fires several requests at once. Without coordination, each 401 — plus the
// route middleware and the boot plugin — launches its OWN POST /auth/refresh.
// Those concurrent refreshes race the backend's refresh-token rotation: they
// read the same current token and rotate to DIFFERENT new tokens, so the cookie
// and the DB end up holding different tokens → the next request 401s with a
// token the server no longer recognizes, and even a page reload stays 401.
// Funnelling all refreshes through ONE in-flight request removes that race at
// the source (the backend CAS rotation covers the remaining cross-tab case).
//
// The in-flight promise is stashed on `nuxtApp`, NOT a module-level variable,
// so it is a true singleton on the client yet request-isolated during SSR (a
// module-level promise would leak across concurrent SSR requests).

const resolveApiBase = (): string => {
  const config = useRuntimeConfig()
  return (
    (import.meta.server && config.apiBaseSsr
      ? (config.apiBaseSsr as string)
      : config.public.apiBase) || 'http://127.0.0.1:9277/api/v1'
  )
}

const doRefresh = async (): Promise<string | null> => {
  try {
    const response = await $fetch<{ code: number; data?: { access_token: string } }>(
      `${resolveApiBase()}/auth/refresh`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include' // browser sends the httpOnly refresh_token cookie
      }
    )
    if (response.code === 0 && response.data) {
      return response.data.access_token
    }
  } catch {
    // network / expired / no cookie — caller decides what to do with null
  }
  return null
}

export const requestTokenRefresh = (): Promise<string | null> => {
  const nuxtApp = useNuxtApp()
  const slot = nuxtApp as unknown as { _authRefreshInFlight?: Promise<string | null> }
  if (!slot._authRefreshInFlight) {
    slot._authRefreshInFlight = doRefresh().finally(() => {
      slot._authRefreshInFlight = undefined
    })
  }
  return slot._authRefreshInFlight
}
