import {
  generateCodeChallenge,
  generateCodeVerifier,
  generateState
} from '~/utils/oauth-pkce'

// Kicks off the OAuth Authorization Code + PKCE flow against our IdP (the SSO
// login the ecosystem sites use). Stashes code_verifier + state + the post-login
// redirect in sessionStorage (validated on /auth/callback), then does a
// TOP-LEVEL navigation to the IdP. Client-only (PKCE uses Web Crypto).
// Contract: docs/integration/oauth/oauth-integration-guide.md.
export const useOAuthLogin = () => {
  const config = useRuntimeConfig()

  const isSafe = (r?: string | null): r is string =>
    !!r && r.startsWith('/') && !r.startsWith('//')

  // Build the authorize URL and persist the PKCE/state/redirect for the callback.
  const buildAuthorizeUrl = async (redirect?: string): Promise<string> => {
    const verifier = generateCodeVerifier()
    const challenge = await generateCodeChallenge(verifier)
    const state = generateState()

    sessionStorage.setItem('oauth_code_verifier', verifier)
    sessionStorage.setItem('oauth_state', state)
    if (isSafe(redirect)) sessionStorage.setItem('oauth_redirect', redirect)
    else sessionStorage.removeItem('oauth_redirect')

    const params = new URLSearchParams({
      response_type: 'code',
      client_id: config.public.oauthClientId,
      redirect_uri: config.public.oauthRedirectUri,
      scope: 'openid profile email',
      state,
      code_challenge: challenge,
      code_challenge_method: 'S256'
    })
    return `${config.public.oauthAuthorizeBase}/oauth/authorize?${params.toString()}`
  }

  // Log in: navigate straight to the IdP authorize endpoint.
  const startLogin = async (redirect?: string) => {
    window.location.href = await buildAuthorizeUrl(redirect)
  }

  // Register: route through the OP register page, which forwards to authorize on
  // success (first-party auto_consent → back to us logged in). Same PKCE code.
  const startRegister = async (redirect?: string) => {
    const authorizeUrl = await buildAuthorizeUrl(redirect)
    window.location.href = `${config.public.oauthWebBase}/auth/register?redirect=${encodeURIComponent(authorizeUrl)}`
  }

  return { startLogin, startRegister }
}
