// Multi-account "session bag" composable (OP-side account switching).
//
// The bag = the set of accounts logged in on THIS browser. Backend contract:
//   GET  /auth/sessions        → list the bag
//   POST /auth/sessions/switch → make `sub` the active account (rotates the
//                                httpOnly refresh cookie + returns a fresh
//                                access_token, same shape as login)
//   POST /auth/sessions/logout      → drop one account from the bag
//   POST /auth/sessions/logout-all  → empty the bag
// See docs/integration/oauth/09.
//
// ⚠️ switchAccount deliberately bypasses useApi: switch returns business-
// meaningful 401s (10016 step-up required / 10005 not in bag / 10001 caller
// not a bag member), and useApi.handleUnauthorized() blindly treats ANY 401
// as a dead session → refresh + navigateTo('/auth/login'), clobbering the
// OAuth flow. We use a raw $fetch (same pattern as useAuth.refreshAccessToken)
// so we can read the body `code` and branch on it ourselves.

// Business error codes returned by POST /auth/sessions/switch on 401.
const CODE_STEP_UP_REQUIRED = 10016

interface SwitchSuccess {
  ok: true
  user: User
}

interface SwitchFailure {
  ok: false
  // step-up required: target is a privileged account → must re-authenticate
  // (not a silent switch). Callers redirect to /auth/login.
  stepUp?: boolean
}

type SwitchResult = SwitchSuccess | SwitchFailure

export const useAccountSwitch = () => {
  const api = useApi()
  const userStore = useUserStore()

  const accessToken = useCookie('access_token', {
    maxAge: 60 * 15, // 15 minutes — mirror useAuth's cookie options
    sameSite: 'lax',
    secure: !import.meta.dev,
  })

  // GET /auth/sessions — the accounts logged in on this browser. Routed
  // through useApi (its 401 handling is correct here: an unauthenticated
  // caller genuinely has no bag).
  const listBagSessions = async (): Promise<BagSession[]> => {
    const response = await api.get<{ items: BagSession[] }>('/auth/sessions')
    if (response.code === 0 && response.data) {
      return response.data.items ?? []
    }
    return []
  }

  // POST /auth/sessions/switch — see the ⚠️ note above for why this is a
  // raw $fetch instead of useApi. On success it mirrors useAuth.login():
  // store the rotated access_token + push the new user into the store, so
  // the rest of the app is now acting as that account.
  const switchAccount = async (sub: string): Promise<SwitchResult> => {
    const config = useRuntimeConfig()
    const baseUrl = config.public.apiBase || 'http://127.0.0.1:9277/api/v1'
    try {
      const response = await $fetch<{ code: number; data: LoginResponse }>(
        `${baseUrl}/auth/sessions/switch`,
        {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            ...(accessToken.value
              ? { Authorization: `Bearer ${accessToken.value}` }
              : {}),
          },
          body: JSON.stringify({ sub }),
          credentials: 'include',
        }
      )
      if (response.code === 0 && response.data) {
        accessToken.value = response.data.access_token
        userStore.setUser(response.data.user)
        // Flush the cookie write so a subsequent full-page navigation reads the
        // NEW token. A useCookie('access_token') ref created in ANOTHER composable
        // (e.g. the authorize page's useApi) does NOT observe this write in-place
        // and refreshCookie doesn't reliably cross-sync it — so callers RELOAD
        // after a switch (the header switcher + the OAuth authorize flow both do),
        // and a fresh useApi then reads the new token. Without that, the immediate
        // consent posts with the OLD account's Bearer (the "two clicks" bug).
        await nextTick()
        return { ok: true, user: response.data.user }
      }
      return { ok: false }
    } catch (error: unknown) {
      // Read the business code off the error body. 401 + 10016 = step-up.
      const fetchError = error as {
        statusCode?: number
        data?: { code?: number }
      }
      if (
        fetchError.statusCode === 401 &&
        fetchError.data?.code === CODE_STEP_UP_REQUIRED
      ) {
        return { ok: false, stepUp: true }
      }
      return { ok: false }
    }
  }

  // POST /auth/sessions/logout — drop a single account from the bag. Its
  // 401s are not expected (a non-member caller), so useApi is fine here.
  const logoutAccount = async (sub: string) => {
    return api.post('/auth/sessions/logout', { sub })
  }

  // POST /auth/sessions/logout-all — empty the bag.
  const logoutAllAccounts = async () => {
    return api.post('/auth/sessions/logout-all')
  }

  return {
    listBagSessions,
    switchAccount,
    logoutAccount,
    logoutAllAccounts,
  }
}
