// Restore the session on client startup, BEFORE route middleware and components
// decide we're logged out.
//
// Why this is needed: the refresh_token is an httpOnly cookie (Path=/api/v1/auth)
// that only the browser can present — it isn't available to the Nuxt SSR server.
// So on a full page load with an expired/absent access_token but a still-valid
// refresh_token, the server can't restore the session; without this the user
// appears logged out until they manually refresh the page ("refresh to log in").
//
// Runs once, client-only, awaited before the app mounts, so the access token +
// user are in place when the auth middleware / layout guard run. The fast path
// (active session: token cookie present and user already hydrated from the
// persisted store) skips the network round-trip.
export default defineNuxtPlugin(async (nuxtApp) => {
  const auth = useAuth()
  const accessToken = useCookie('access_token')

  if (accessToken.value && auth.user.value) {
    return
  }

  // fetchUser() refreshes via the httpOnly cookie when there's no access token,
  // then loads /auth/me. Failure is silent (no session) — the layout's mount
  // guard / middleware handle the redirect to /auth/login.
  const user = await auth.fetchUser()

  // If we restored a session the server couldn't (no usable token at SSR time),
  // the page's SSR data was fetched unauthenticated and came back empty. Refetch
  // once the app is mounted, now that the access token is in place — otherwise
  // the user lands logged-in but on an empty page.
  if (user) {
    nuxtApp.hook('app:mounted', () => {
      refreshNuxtData()
    })
  }
})
