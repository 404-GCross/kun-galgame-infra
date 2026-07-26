import {
  REFRESH_TRANSIENT,
  requestTokenRefresh
} from '../composables/useTokenRefresh'

export default defineNuxtRouteMiddleware(async (to) => {
  const accessToken = useCookie('access_token')

  // If an access token is present, proceed (validity is re-checked by the
  // layout/page reads, which refresh on 401).
  if (accessToken.value) {
    return
  }

  // No access token. The refresh_token is an httpOnly cookie scoped to
  // /api/v1/auth — the browser never sends it to the Nuxt SSR server on a page
  // navigation (path mismatch), so a SERVER-side refresh always fails.
  // Redirecting here would bounce a returning user with a valid session to
  // /login on every full page load. Defer to the client, where the auth.client
  // plugin restores the session on startup and this middleware refreshes on
  // client-side navigations.
  if (import.meta.server) {
    return
  }

  const auth = useAuth()
  const result = await requestTokenRefresh()
  if (typeof result === 'string') {
    auth.setAccessToken(result)
    return
  }

  // Transient failure (network blip / IdP 5xx): the session is still alive, so
  // bouncing to /login here would violate the REFRESH_TRANSIENT contract. Let
  // the navigation through — the page renders degraded and the global retry
  // banner (layout/RefreshBanner) offers recovery.
  if (result === REFRESH_TRANSIENT) {
    return
  }

  // Dead session: to /login, preserving the destination.
  const here = to.fullPath
  return navigateTo(`/login?redirect=${encodeURIComponent(here)}`)
})
