// Session management for the wiki admin.
// Login itself is handled by useOAuthLogin() (Authorization Code + PKCE);
// this composable handles refresh / logout / fetch-user.
//
// Endpoints used (all under apps/api OAuth backend):
//   - POST /oauth/token   grant_type=refresh_token  → refresh access_token
//   - POST /oauth/revoke                            → invalidate refresh_token
//   - GET  /auth/me                                 → load user profile
//
// We do NOT call /auth/refresh or /auth/logout — those belong to the
// "regular" (non-OAuth) session that the OAuth admin frontend uses;
// hitting them from wiki would either fail silently or revoke an
// unrelated session.
import type { User } from '~/shared/types/user'

export const useAuth = () => {
  const authApi = useAuthApi()
  const userStore = useUserStore()
  const config = useRuntimeConfig()

  // Wiki uses its own cookie name to avoid colliding with apps/web and
  // the OAuth admin frontend (both of which use 'access_token'). In dev
  // every app runs on localhost and would share cookies otherwise —
  // logging out of one would log out of all, and logging in to one
  // would auto-log in to the others.
  const accessToken = useCookie('wiki_access_token', {
    maxAge: 60 * 15,
    sameSite: 'lax',
    secure: !import.meta.dev
  })
  const refreshToken = useCookie('wiki_refresh_token', {
    // 90d — matches the OAuth server's default refresh_token TTL.
    // Keep in sync with useOAuthLogin.ts.
    maxAge: 60 * 60 * 24 * 90,
    sameSite: 'lax',
    secure: !import.meta.dev
  })

  const clearAuth = () => {
    accessToken.value = null
    refreshToken.value = null
    userStore.clearUser()
  }

  // Revoke this RP's refresh_token (RFC 7009) and clear local cookies + store.
  // Public client — no client_secret. Revoke is best-effort: a server failure
  // must not block the local clear. Shared by both logout modes.
  const clearLocalSession = async () => {
    if (refreshToken.value) {
      try {
        await authApi.post('/oauth/revoke', { token: refreshToken.value })
      } catch {
        // Server-side revocation is best-effort; local clear still runs.
      }
    }
    clearAuth()
  }

  // "This site only" — end the wiki session but KEEP the central OP (SSO)
  // session. Other sites stay logged in, and logging back in here is silent
  // (the OP still has the session, so /oauth/authorize auto-consents). Good for
  // the user's own device.
  const logoutLocal = async () => {
    await clearLocalSession()
    return navigateTo('/auth/login')
  }

  // "Everywhere" — end the wiki session AND the central OP session via
  // RP-initiated logout. Clearing our refresh_token + local cookies alone is
  // NOT enough: the OP session (cross-origin localStorage user + cross-site
  // refresh cookie) survives, so the next login would silently re-consent into
  // the same account. Top-level navigate to the OP logout entrypoint (symmetric
  // with the /oauth/authorize login redirect) so the OP clears its session,
  // then returns here. No site can silently re-login afterwards, and other
  // sites' sessions lapse on their next refresh. See
  // docs/integration/oauth/07-logout.md.
  const logoutEverywhere = async () => {
    await clearLocalSession()
    if (import.meta.client) {
      const params = new URLSearchParams({
        client_id: config.public.oauthClientID as string,
        redirect: window.location.origin + '/'
      })
      window.location.href = `${config.public.oauthAuthorizeBase}/oauth/logout?${params.toString()}`
      return
    }
    return navigateTo('/auth/login')
  }

  // Refresh via the SHARED, single-flighted /oauth/token grant so this path,
  // useApi's 401 retry, and the route middleware never fire competing refreshes
  // (which would race the server's token rotation). On definitive failure clear
  // the local session — the persisted user store must not outlive a dead
  // refresh token, or `isLoggedIn` stays stale-true and login/redirect loops.
  const refreshAccessToken = async () => {
    const token = await requestTokenRefresh()
    if (token) {
      accessToken.value = token
      return true
    }
    clearAuth()
    return false
  }

  const fetchUser = async () => {
    if (!accessToken.value) {
      const refreshed = await refreshAccessToken()
      if (!refreshed) return null
    }

    const response = await authApi.get<User>('/auth/me')
    if (response.code === 0) {
      userStore.setUser(response.data)
      return response.data
    }
    return null
  }

  return {
    user: computed(() => userStore.user),
    isLoggedIn: computed(() => userStore.isLoggedIn),
    isAdmin: computed(() => userStore.isAdmin),
    clearAuth,
    logoutLocal,
    logoutEverywhere,
    fetchUser,
    refreshAccessToken
  }
}
