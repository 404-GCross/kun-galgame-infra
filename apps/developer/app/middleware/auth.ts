import {
  REFRESH_TRANSIENT,
  requestTokenRefresh
} from '../composables/useTokenRefresh'

export default defineNuxtRouteMiddleware(async (to) => {
  const accessToken = useCookie('access_token')

  if (accessToken.value) {
    return
  }

  if (import.meta.server) {
    return
  }

  const auth = useAuth()
  const result = await requestTokenRefresh()
  if (typeof result === 'string') {
    auth.setAccessToken(result)
    return
  }

  if (result === REFRESH_TRANSIENT) {
    return
  }

  const here = to.fullPath
  return navigateTo(`/login?redirect=${encodeURIComponent(here)}`)
})
