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

  // User must have admin role — kick non-admins back to login
  if (!auth.isAdmin.value) {
    auth.user.value = null
    useCookie('access_token').value = null
    return navigateTo('/auth/login')
  }
})
