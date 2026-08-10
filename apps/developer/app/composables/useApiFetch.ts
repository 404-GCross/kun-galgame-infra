import type { UseFetchOptions } from '#app'
import { API_BASE } from './useApi'

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
