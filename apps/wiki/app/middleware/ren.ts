// ren(莲)-only gate for the internal catalog browser. Mirrors the galgame
// backend proxy's RequireRole("ren") — the backend is the real enforcement
// (this just avoids showing a page whose data would 403). Pair with the
// `auth` middleware, which runs first.
export default defineNuxtRouteMiddleware(async () => {
  const auth = useAuth()
  if (!auth.user.value) {
    await auth.fetchUser()
  }
  const roles = auth.user.value?.roles ?? []
  if (!roles.includes('ren')) {
    return navigateTo('/')
  }
})
