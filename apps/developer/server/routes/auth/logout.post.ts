export default defineEventHandler(async (event) => {
  const refreshToken = getCookie(event, 'refresh_token')
  if (refreshToken) {
    const config = useRuntimeConfig(event)
    await $fetch(`${config.oauthApiBase}/api/v1/oauth/revoke`, {
      method: 'POST',
      body: { token: refreshToken }
    }).catch(() => {
    })
  }
  clearOAuthSession(event)
  return { code: 0 }
})
