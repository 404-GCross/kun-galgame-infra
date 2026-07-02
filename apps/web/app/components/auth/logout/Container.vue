<script setup lang="ts">
// RP-Initiated Logout landing page on the OP (oauth.kungal.com).
//
// Downstream sites (wiki / patch / forum) can't clear the OP session
// themselves — localStorage is per-origin and the refresh cookie won't cross
// sites. So on user logout they TOP-LEVEL navigate the browser here; this page
// clears the central session, then redirects back to a validated RP URL.
// See docs/integration/oauth/07-logout.md.
const route = useRoute()
const auth = useAuth()
const api = useApi()

onMounted(async () => {
  // 1. Clear the OP session: delete the server session + refresh cookie, and
  //    wipe the OP frontend's persisted user + access_token (so the next
  //    /oauth/authorize no longer silently auto-consents).
  await auth.logoutSilent()

  // 2. Resolve a safe post-logout redirect. The server validates `redirect`
  //    against the client's registered redirect_uris (origin match) to prevent
  //    open-redirect; anything unknown falls back to the OP home.
  const clientId = route.query.client_id as string | undefined
  const redirect = route.query.redirect as string | undefined
  const state = route.query.state as string | undefined
  let dest = '/'
  if (clientId && redirect) {
    try {
      const res = await api.get<{ url: string }>('/oauth/post-logout-redirect', {
        client_id: clientId,
        redirect,
      })
      if (res.code === 0 && res.data?.url) {
        dest = res.data.url
        // OIDC RP-Initiated Logout: echo `state` onto the validated redirect
        // (never onto the OP-home fallback) so the RP can correlate the return.
        if (state) {
          const u = new URL(dest)
          u.searchParams.set('state', state)
          dest = u.toString()
        }
      }
    } catch {
      // fall back to OP home
    }
  }
  window.location.href = dest
})
</script>

<template>
  <KunCard class="p-8">
    <div class="py-8 text-center">
      <KunIcon name="lucide:loader-circle" class="text-primary mx-auto mb-3 size-8 animate-spin" />
      <p class="text-default-500 text-sm">正在登出...</p>
    </div>
  </KunCard>
</template>
