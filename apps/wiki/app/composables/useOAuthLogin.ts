// OAuth 2.0 Authorization Code flow with PKCE for the wiki admin.
//
// Flow:
//   1. login()   — generates PKCE verifier + state, persists them,
//                  redirects browser to the oauth authorize endpoint
//   2. (oauth backend redirects to oauth frontend consent page, user approves)
//   3. (oauth frontend redirects back to /auth/callback?code=&state=)
//   4. handleCallback() — verifies state, exchanges code for access_token,
//                         stores token, fetches user, then routes to /

const VERIFIER_KEY = 'kun_wiki_oauth_verifier'
const STATE_KEY = 'kun_wiki_oauth_state'

const randomString = (bytes: number) => {
  const arr = new Uint8Array(bytes)
  crypto.getRandomValues(arr)
  // base64url without padding
  return btoa(String.fromCharCode(...arr))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')
}

const sha256Base64Url = async (input: string) => {
  const digest = await crypto.subtle.digest(
    'SHA-256',
    new TextEncoder().encode(input)
  )
  return btoa(String.fromCharCode(...new Uint8Array(digest)))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')
}

export const useOAuthLogin = () => {
  const config = useRuntimeConfig()
  const authApi = useAuthApi()
  const auth = useAuth()

  // Wiki-scoped cookie names — see useAuth.ts for the collision rationale.
  const accessToken = useCookie('wiki_access_token', {
    maxAge: 60 * 15,
    sameSite: 'lax',
    secure: !import.meta.dev
  })
  const refreshToken = useCookie('wiki_refresh_token', {
    maxAge: 60 * 60 * 24 * 7, // 7d, matches server-side JWT exp
    sameSite: 'lax',
    secure: !import.meta.dev
  })

  const login = async () => {
    if (!import.meta.client) return

    const verifier = randomString(48)
    const state = randomString(16)
    const codeChallenge = await sha256Base64Url(verifier)

    sessionStorage.setItem(VERIFIER_KEY, verifier)
    sessionStorage.setItem(STATE_KEY, state)

    const params = new URLSearchParams({
      client_id: config.public.oauthClientID as string,
      redirect_uri: config.public.oauthRedirectURI as string,
      response_type: 'code',
      scope: config.public.oauthScope as string,
      state,
      code_challenge: codeChallenge,
      code_challenge_method: 'S256'
    })

    window.location.href = `${config.public.oauthAuthorizeBase}/oauth/authorize?${params.toString()}`
  }

  const handleCallback = async (code: string, state: string) => {
    if (!import.meta.client) {
      return { ok: false, error: '仅在客户端可用' }
    }

    const savedVerifier = sessionStorage.getItem(VERIFIER_KEY)
    const savedState = sessionStorage.getItem(STATE_KEY)

    if (!savedVerifier || !savedState) {
      return { ok: false, error: '登录上下文丢失，请重新发起登录' }
    }
    if (savedState !== state) {
      return { ok: false, error: 'state 不匹配，可能的 CSRF 攻击' }
    }

    sessionStorage.removeItem(VERIFIER_KEY)
    sessionStorage.removeItem(STATE_KEY)

    const response = await authApi.post<{
      access_token: string
      refresh_token?: string
      token_type: string
      expires_in: number
    }>('/oauth/token', {
      grant_type: 'authorization_code',
      code,
      client_id: config.public.oauthClientID as string,
      redirect_uri: config.public.oauthRedirectURI as string,
      code_verifier: savedVerifier
    })

    if (response.code !== 0 || !response.data?.access_token) {
      return {
        ok: false,
        error: response.message || '换取 token 失败'
      }
    }

    accessToken.value = response.data.access_token
    if (response.data.refresh_token) {
      refreshToken.value = response.data.refresh_token
    }

    // The OAuth access_token is the same JWT used by the regular login flow
    // (shared JWT secret), so /auth/me works directly and returns the full User
    // including roles, moemoepoint, etc.
    await auth.fetchUser()

    return { ok: true as const }
  }

  return { login, handleCallback }
}
