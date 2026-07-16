import type { UseFetchOptions } from '#app'
import { API_BASE } from './useApi'

// SSR-safe data fetching for authenticated GET reads (dashboard / app detail).
// useFetch runs on BOTH server and client, so awaiting it in setup lands the
// payload in the server-rendered HTML. The access_token is a non-httpOnly cookie
// (Path=/), so useCookie reads it on the server too and we forward it as
// `Authorization: Bearer`. If the token is missing/expired at SSR time the API
// 401s, useFetch surfaces `error` + the caller's default, and the client plugin
// / useApi handle re-auth — no SSR crash.
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
  options: UseFetchOptions<ApiResponse<T>, T | null> = {}
) => {
  const accessToken = useCookie('access_token')

  return useFetch(url, {
    baseURL: API_BASE,
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
    ...options
  })
}
