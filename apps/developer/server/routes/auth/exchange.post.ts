// Server-side OAuth Authorization-Code exchange for the developer-portal SSO
// login. The /auth/callback page posts { code, code_verifier }; we swap it for
// tokens at the IdP token endpoint with the confidential client_secret (SERVER
// ONLY), then land the session cookies (see server/utils/oauth-session). The
// access_token is returned in the body so the client can also seed its cookie
// ref immediately; the refresh_token stays httpOnly and never reaches the JS.
// Contract: docs/integration/oauth/01-oauth-endpoints.md §POST /oauth/token.
interface TokenEnvelope {
  code: number
  message?: string
  data?: {
    access_token: string
    refresh_token?: string
    expires_in?: number
    token_type?: string
    scope?: string
  }
}

export default defineEventHandler(async (event) => {
  const body = await readBody<{ code?: string; code_verifier?: string }>(event)
  if (!body?.code || !body?.code_verifier) {
    setResponseStatus(event, 400)
    return { code: 8, message: '缺少授权码' }
  }

  const config = useRuntimeConfig(event)
  const res = await $fetch<TokenEnvelope>(
    `${config.oauthApiBase}/api/v1/oauth/token`,
    {
      method: 'POST',
      body: {
        grant_type: 'authorization_code',
        code: body.code,
        redirect_uri: config.public.oauthRedirectUri,
        client_id: config.public.oauthClientId,
        client_secret: config.oauthClientSecret,
        code_verifier: body.code_verifier
      }
    }
  ).catch(
    (e: { data?: TokenEnvelope }): TokenEnvelope =>
      e?.data ?? { code: -1, message: '换取令牌失败' }
  )

  if (res.code !== 0 || !res.data?.access_token) {
    setResponseStatus(event, 400)
    return { code: res.code || -1, message: res.message || '登录失败' }
  }

  landOAuthSession(event, res.data)
  return { code: 0, data: { access_token: res.data.access_token } }
})
