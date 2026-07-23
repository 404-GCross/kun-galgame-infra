// OAuth (SSO) logout: best-effort revoke the refresh_token at the IdP (RFC 7009
// — always 200), then clear the local session cookies. The client calls this
// only for auth_mode=oauth sessions; the password fallback uses the first-party
// /api/v1/auth/logout via the relay.
export default defineEventHandler(async (event) => {
  const refreshToken = getCookie(event, 'refresh_token')
  if (refreshToken) {
    const config = useRuntimeConfig(event)
    await $fetch(`${config.oauthApiBase}/api/v1/oauth/revoke`, {
      method: 'POST',
      body: { token: refreshToken }
    }).catch(() => {
      // Revocation is best-effort; local cookies are cleared regardless.
    })
  }
  clearOAuthSession(event)
  return { code: 0 }
})
