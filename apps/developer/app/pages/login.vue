<script setup lang="ts">
// Stable login entry (bookmarks, guarded-route + 401 redirects). The portal's
// login UI is a single modal (useLoginModal / LoginModal in app.vue) — this
// route renders the landing and pops that modal over it, carrying any
// ?redirect target. A signed-in visitor is sent straight to the console.
const { open } = useLoginModal()
const route = useRoute()

// Key the redirect decision off the SAME signal the auth middleware trusts (the
// access_token cookie), not the persisted user store — otherwise a stale store
// (session gone but store not cleared) would bounce /login → /dashboard →
// /login forever. Token present → console; absent → open the modal here.
const accessToken = useCookie('access_token')

onMounted(() => {
  if (accessToken.value) {
    navigateTo('/dashboard')
    return
  }
  open(route.query.redirect as string | undefined)
})
</script>

<template>
  <LandingContainer />
</template>
