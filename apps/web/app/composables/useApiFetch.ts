import type { UseFetchOptions } from '#app'
import { resolveApiBase, type ApiService } from './useApi'

// SSR-safe data fetching, mirroring kungal's useKunFetch pattern but for the
// admin's Bearer-token auth. Use it for GET reads that should render on first
// paint (SSR); keep useApi() for mutations (POST/PUT/DELETE).
//
// Why this gives SSR data: useFetch runs on BOTH server and client, so when a
// component awaits it in setup the payload lands in the server-rendered HTML
// (no onMounted client-only flash).
//
// Auth: the access_token is a non-httpOnly cookie, so useCookie reads it on
// the server (from the incoming request) as well as the client. We forward it
// as `Authorization: Bearer` on every request. If the token is missing/expired
// at SSR time the API 401s, useFetch surfaces `error` + falls back to the
// caller's `default`, and the route middleware / client handles re-auth — no
// SSR crash.
//
// transform unwraps the Go `{ code, message, data }` envelope; on a non-zero
// code it yields null so callers can coalesce to a safe default.
interface ApiResponse<T> {
  code: number
  message: string
  data: T
}

export const useApiFetch = <T>(
  url: string | (() => string),
  options: UseFetchOptions<ApiResponse<T>, T | null> = {},
  service: ApiService = 'oauth'
) => {
  // Dual base per service (oauth default / catalog) — see resolveApiBase.
  const baseURL = resolveApiBase(service)
  const accessToken = useCookie('access_token')

  return useFetch(url, {
    baseURL,
    // A stale-but-present access token 401s on first paint; refresh once (via
    // the shared single-flight helper) and retry so an expired session renders
    // data instead of silently falling back to the caller's empty default.
    // Client-only — SSR has no refresh cookie to spend, so there a 401 falls
    // through to `default` and the client re-fetches. Mirrors useApi.request.
    retry: 1,
    retryStatusCodes: [401],
    onRequest({ options: requestOptions }) {
      if (accessToken.value) {
        const headers = new Headers(
          requestOptions.headers as HeadersInit | undefined
        )
        headers.set('Authorization', `Bearer ${accessToken.value}`)
        requestOptions.headers = headers
      }
    },
    async onResponseError({ response }) {
      if (import.meta.client && response.status === 401) {
        const token = await requestTokenRefresh()
        if (token) accessToken.value = token
      }
    },
    transform: (resp: ApiResponse<T>) =>
      resp && resp.code === 0 ? resp.data : null,
    ...options,
  })
}
