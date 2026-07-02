// Single-flighted access-token refresh for the wiki, shared by EVERY refresh
// path: route middleware, useAuth.fetchUser/refreshAccessToken, and useApi's
// 401 retry. Returns the new access_token, or null.
//
// This is the wiki's ONE correct refresh: the OAuth client refresh-token grant
// (grant_type=refresh_token against /oauth/token, using the wiki_refresh_token
// cookie). It deliberately does NOT use /auth/refresh — that targets the IdP's
// SEPARATE first-party session (httpOnly cookie) and rejects this client-bound
// session; useApi used to call it by mistake, which only "worked" on localhost
// (same-site) by borrowing the first-party session and failed cross-site in
// prod. See useAuth.ts.
//
// WHY single-flight: when the 15-min access_token expires, navigation fires the
// route middleware + several data fetches at once; without coordination each
// 401 launches its OWN /oauth/token refresh. Those concurrent refreshes race
// the server's refresh-token rotation. The backend now rotates atomically (CAS),
// but funnelling all callers through ONE in-flight refresh removes the storm at
// the source and keeps the rotated wiki_refresh_token cookie consistent.
//
// The in-flight promise lives on nuxtApp (not a module-level var) so it's a
// singleton on the client yet request-isolated during SSR.

const doRefresh = async (): Promise<string | null> => {
  const config = useRuntimeConfig()
  const refreshToken = useCookie('wiki_refresh_token', {
    maxAge: 60 * 60 * 24 * 90, // 90d — match the server's per-client TTL
    sameSite: 'lax',
    secure: !import.meta.dev
  })
  if (!refreshToken.value) return null

  // useAuthApi targets the oauth backend and never throws (it returns an
  // envelope), so no try/catch is needed here.
  const authApi = useAuthApi()
  const response = await authApi.post<{ access_token: string; refresh_token?: string }>(
    '/oauth/token',
    {
      grant_type: 'refresh_token',
      refresh_token: refreshToken.value,
      client_id: config.public.oauthClientID as string
    }
  )
  // Tolerant reader (OAuth server's standard-wire cutover): the token payload is
  // either the legacy envelope's `data` or the standard top-level body.
  const tok = (response.data as { access_token?: string })?.access_token
    ? response.data
    : (response as unknown as { access_token?: string; refresh_token?: string })
  if (tok?.access_token) {
    // The server rotates the refresh_token; persist the new one so the next
    // refresh presents it (and the old one falls into the server's grace window).
    if (tok.refresh_token) {
      refreshToken.value = tok.refresh_token
    }
    return tok.access_token
  }
  return null
}

export const requestTokenRefresh = (): Promise<string | null> => {
  const nuxtApp = useNuxtApp()
  const slot = nuxtApp as unknown as { _wikiAuthRefreshInFlight?: Promise<string | null> }
  if (!slot._wikiAuthRefreshInFlight) {
    slot._wikiAuthRefreshInFlight = doRefresh().finally(() => {
      slot._wikiAuthRefreshInFlight = undefined
    })
  }
  return slot._wikiAuthRefreshInFlight
}
