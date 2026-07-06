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
    onRequest({ options: requestOptions }) {
      if (accessToken.value) {
        const headers = new Headers(
          requestOptions.headers as HeadersInit | undefined
        )
        headers.set('Authorization', `Bearer ${accessToken.value}`)
        requestOptions.headers = headers
      }
    },
    transform: (resp: ApiResponse<T>) =>
      resp && resp.code === 0 ? resp.data : null,
    ...options,
  })
}
