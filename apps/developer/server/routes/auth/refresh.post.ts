// OAuth (SSO) access-token refresh — the OAuth counterpart of the first-party
// /api/v1/auth/refresh. It CANNOT reuse that endpoint: the IdP rejects
// client-bound (OAuth) sessions there (auth_service.go:611), so an OAuth session
// must refresh through /oauth/token with grant_type=refresh_token. Reads the
// httpOnly refresh_token cookie, rotates it, and re-lands the session.
//
// §4.4 discipline: only a PERMANENT failure (the IdP returned an error body —
// invalid_grant / expired) clears the session; a transient network/5xx blip
// keeps the cookies so the caller can retry. The client single-flights refreshes
// (useTokenRefresh), so concurrent 401s collapse into one call here.
// Token responses are read via the tokenWire helpers, which judge success by
// the presence of access_token in the bare RFC 6749 shape.
import {
  tokenWireError,
  tokenWirePayload,
  type TokenWire
} from '../../utils/oauth-session'

export default defineEventHandler(async (event) => {
  const refreshToken = getCookie(event, 'refresh_token')
  if (!refreshToken) {
    setResponseStatus(event, 401)
    return { code: 10003, message: '会话已过期' }
  }

  const config = useRuntimeConfig(event)
  let res: TokenWire
  try {
    res = await $fetch<TokenWire>(
      `${config.oauthApiBase}/api/v1/oauth/token`,
      {
        method: 'POST',
        body: {
          grant_type: 'refresh_token',
          refresh_token: refreshToken,
          client_id: config.public.oauthClientId,
          client_secret: config.oauthClientSecret
        }
      }
    )
  } catch (e) {
    const data = (e as { data?: TokenWire })?.data
    if (!data) {
      // Transient (network / 5xx) — keep the session, let the client retry.
      setResponseStatus(event, 503)
      return { code: -1, message: '刷新暂时失败' }
    }
    res = data // 4xx with an error body → treated as permanent below.
  }

  const tokens = tokenWirePayload(res)
  if (!tokens) {
    clearOAuthSession(event) // permanent: refresh token dead → force re-login.
    setResponseStatus(event, 401)
    return { code: 10003, message: tokenWireError(res) || '会话已过期' }
  }

  landOAuthSession(event, tokens) // rotation writes the new refresh_token.
  return { code: 0, data: { access_token: tokens.access_token } }
})
