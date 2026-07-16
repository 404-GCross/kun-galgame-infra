// Restore the session on client startup, BEFORE route middleware and components
// decide we're logged out.
//
// The refresh_token is an httpOnly cookie (Path=/api/v1/auth) that only the
// browser can present — it isn't available to the Nuxt SSR server. So on a full
// page load with an expired/absent access_token but a still-valid refresh_token,
// the server can't restore the session; without this the user appears logged out
// until they manually refresh the page.
//
// Runs once, client-only, awaited before the app mounts. The fast path (active
// session: token cookie present and user already hydrated from the persisted
// store) skips the network round-trip.
export default defineNuxtPlugin(async (nuxtApp) => {
  const auth = useAuth()
  const accessToken = useCookie('access_token')

  if (accessToken.value && auth.user.value) {
    return
  }

  const user = await auth.fetchUser()

  // If we restored a session the server couldn't (no usable token at SSR time),
  // the page's SSR data was fetched unauthenticated and came back empty. Refetch
  // once mounted, now that the access token is in place.
  if (user) {
    nuxtApp.hook('app:mounted', () => {
      refreshNuxtData()
    })
  }
})
