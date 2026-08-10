import {
  generateCodeChallenge,
  generateCodeVerifier,
  generateState
} from '~/utils/oauth-pkce'
import { isSafeInternalPath } from '~/utils/safe-path'

export const useOAuthLogin = () => {
  const config = useRuntimeConfig()

  const buildAuthorizeUrl = async (redirect?: string): Promise<string> => {
    const verifier = generateCodeVerifier()
    const challenge = await generateCodeChallenge(verifier)
    const state = generateState()

    sessionStorage.setItem('oauth_code_verifier', verifier)
    sessionStorage.setItem('oauth_state', state)
    if (isSafeInternalPath(redirect))
      sessionStorage.setItem('oauth_redirect', redirect)
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

  const startLogin = async (redirect?: string) => {
    window.location.href = await buildAuthorizeUrl(redirect)
  }

  const startRegister = async (redirect?: string) => {
    const authorizeUrl = await buildAuthorizeUrl(redirect)
    window.location.href = `${config.public.oauthWebBase}/auth/register?redirect=${encodeURIComponent(authorizeUrl)}`
  }

  return { startLogin, startRegister }
}
