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

  const logout = async () => {
    // Revoke the refresh_token at OAuth server (RFC 7009). Public client —
    // no client_secret required. Errors are swallowed so a server-side
    // failure doesn't block the local logout.
    if (refreshToken.value) {
      try {
        await authApi.post('/oauth/revoke', { token: refreshToken.value })
      } catch {
        // Server-side revocation is best-effort; local clear still runs.
      }
    }
    clearAuth()

    // RP-initiated logout: revoking our own refresh_token + clearing local
    // cookies is NOT enough — the central OP (oauth.kungal.com) SSO session
    // survives (its localStorage user is cross-origin, its refresh cookie is
    // cross-site), so the next login would silently re-consent into the same
    // account. Top-level navigate to the OP logout entrypoint (symmetric with
    // the /oauth/authorize login redirect) so the OP clears its session, then
    // returns here. See docs/integration/oauth/07-logout.md.
    if (import.meta.client) {
      const params = new URLSearchParams({
        client_id: config.public.oauthClientID as string,
        redirect: window.location.origin + '/'
      })
      window.location.href = `${config.public.oauthAuthorizeBase}/oauth/logout?${params.toString()}`
      return
    }
    navigateTo('/auth/login')
  }

  const refreshAccessToken = async () => {
    if (!refreshToken.value) {
      // No refresh_token to use — caller must re-run the OAuth flow.
      return false
    }
    try {
      const response = await authApi.post<{
        access_token: string
        refresh_token?: string
      }>('/oauth/token', {
        grant_type: 'refresh_token',
        refresh_token: refreshToken.value,
        client_id: config.public.oauthClientID as string
      })
      if (response.code === 0 && response.data?.access_token) {
        accessToken.value = response.data.access_token
        // Server may rotate refresh_token; if it returns a new one, save it.
        if (response.data.refresh_token) {
          refreshToken.value = response.data.refresh_token
        }
        return true
      }
    } catch {
      clearAuth()
    }
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
    logout,
    fetchUser,
    refreshAccessToken
  }
}
