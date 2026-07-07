// staff gate for the internal catalog browser: admin / moderator / ren only.
// Mirrors the galgame backend proxy's RequireRole("admin","moderator") — the
// backend is the real enforcement (this just avoids showing a page whose data
// would 403). Pair with the `auth` middleware, which runs first.
export default defineNuxtRouteMiddleware(async () => {
  const auth = useAuth()
  if (!auth.user.value) {
    await auth.fetchUser()
  }
  const roles = auth.user.value?.roles ?? []
  const isStaff = ['ren', 'admin', 'moderator'].some((r) => roles.includes(r))
  if (!isStaff) {
    return navigateTo('/')
  }
})
