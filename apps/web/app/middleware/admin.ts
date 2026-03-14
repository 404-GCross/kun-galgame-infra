export default defineNuxtRouteMiddleware(async () => {
  const auth = useAuth()

  // Ensure user is loaded
  if (!auth.user.value) {
    await auth.fetchUser()
  }

  // Check if user exists and has admin privileges
  // Status 0 = active, we could add role checking here
  if (!auth.user.value) {
    return navigateTo('/auth/login')
  }
})
