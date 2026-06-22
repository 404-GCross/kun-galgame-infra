export default defineNuxtRouteMiddleware(async () => {
  const auth = useAuth()

  // Ensure user is loaded
  if (!auth.user.value) {
    await auth.fetchUser()
  }

  // User must exist
  if (!auth.user.value) {
    return navigateTo('/auth/login')
  }

  // ren(莲)-only pages (e.g. artifact file browser / storage config): everyone
  // else — including ordinary admins — goes to their profile.
  if (!auth.isRen.value) {
    return navigateTo('/profile')
  }
})
