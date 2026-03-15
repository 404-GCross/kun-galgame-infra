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

  // User must have admin role
  if (!auth.isAdmin.value) {
    return navigateTo('/')
  }
})
