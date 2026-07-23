// Single-flighted access-token refresh, shared by every refresh path (the boot
// plugin, useAuth.refreshAccessToken, useApi's 401 retry). Returns the new
// access_token (string) on success, REFRESH_TRANSIENT when the failure is
// retryable (network blip / 5xx — the session is still alive and must NOT be
// treated as logged-out), or null when the session is genuinely dead.
//
// The portal is a same-origin shell: /api/v1/auth/refresh is served by this
// app's origin and relayed to oauth by Nitro (server/routes/api). The browser
// presents the httpOnly refresh_token cookie (Path=/api/v1/auth) on that call,
// so the relay carries it to oauth untouched.
//
// The in-flight promise is stashed on `nuxtApp`, NOT a module-level variable, so
// it is a true singleton on the client yet request-isolated during SSR.

export const REFRESH_TRANSIENT = Symbol('refresh-transient')
export type RefreshResult = string | typeof REFRESH_TRANSIENT | null

const doRefresh = async (): Promise<RefreshResult> => {
  // OAuth (SSO) sessions refresh via our Nitro route (which wraps /oauth/token —
  // the first-party /api/v1/auth/refresh rejects client-bound sessions); the
  // password fallback uses the relayed first-party endpoint. auth_mode selects.
  const url =
    useCookie('auth_mode').value === 'oauth'
      ? '/auth/refresh'
      : '/api/v1/auth/refresh'
  try {
    const response = await $fetch<{
      code: number
      data?: { access_token: string }
    }>(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include' // browser sends the httpOnly refresh_token cookie
    })
    if (response.code === 0 && response.data) {
      return response.data.access_token
    }
    return null
  } catch (e) {
    // A dead session answers 4xx (no cookie / expired / revoked); anything
    // else — network failure, IdP 5xx, the OAuth refresh route's deliberate
    // 503 — is a transient blip that must not wipe a live session.
    const status = (e as { statusCode?: number }).statusCode
    return !status || status >= 500 ? REFRESH_TRANSIENT : null
  }
}

export const requestTokenRefresh = (): Promise<RefreshResult> => {
  const nuxtApp = useNuxtApp()
  const slot = nuxtApp as unknown as {
    _authRefreshInFlight?: Promise<RefreshResult>
  }
  if (!slot._authRefreshInFlight) {
    slot._authRefreshInFlight = doRefresh().finally(() => {
      slot._authRefreshInFlight = undefined
    })
  }
  return slot._authRefreshInFlight
}
