
export const REFRESH_TRANSIENT = Symbol('refresh-transient')
export type RefreshResult = string | typeof REFRESH_TRANSIENT | null

export const useRefreshTransient = () =>
  useState<boolean>('auth-refresh-transient', () => false)

const doRefresh = async (): Promise<RefreshResult> => {
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
    const status = (e as { statusCode?: number }).statusCode
    return !status || status >= 500 ? REFRESH_TRANSIENT : null
  }
}

export const requestTokenRefresh = (): Promise<RefreshResult> => {
  const nuxtApp = useNuxtApp()
  const transient = useRefreshTransient()
  const slot = nuxtApp as unknown as {
    _authRefreshInFlight?: Promise<RefreshResult>
  }
  if (!slot._authRefreshInFlight) {
    slot._authRefreshInFlight = doRefresh()
      .then((result) => {
        transient.value = result === REFRESH_TRANSIENT
        return result
      })
      .finally(() => {
        slot._authRefreshInFlight = undefined
      })
  }
  return slot._authRefreshInFlight
}
