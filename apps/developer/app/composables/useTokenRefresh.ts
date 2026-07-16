// Single-flighted access-token refresh, shared by every refresh path (the boot
// plugin, useAuth.refreshAccessToken, useApi's 401 retry). Returns the new
// access_token (string) on success, or null.
//
// The portal is a same-origin shell: /api/v1/auth/refresh is served by this
// app's origin and relayed to oauth by Nitro (server/routes/api). The browser
// presents the httpOnly refresh_token cookie (Path=/api/v1/auth) on that call,
// so the relay carries it to oauth untouched.
//
// The in-flight promise is stashed on `nuxtApp`, NOT a module-level variable, so
// it is a true singleton on the client yet request-isolated during SSR.

const doRefresh = async (): Promise<string | null> => {
  try {
    const response = await $fetch<{
      code: number
      data?: { access_token: string }
    }>('/api/v1/auth/refresh', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include' // browser sends the httpOnly refresh_token cookie
    })
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
  const slot = nuxtApp as unknown as {
    _authRefreshInFlight?: Promise<string | null>
  }
  if (!slot._authRefreshInFlight) {
    slot._authRefreshInFlight = doRefresh().finally(() => {
      slot._authRefreshInFlight = undefined
    })
  }
  return slot._authRefreshInFlight
}
