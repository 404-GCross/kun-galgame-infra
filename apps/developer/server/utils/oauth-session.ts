import type { H3Event } from 'h3'

// Cookie plumbing for the OAuth (SSO) session, shared by the exchange / refresh
// / logout server routes. Lands the IdP's tokens into the portal's cookie
// convention:
//   - access_token : JS-readable (Path=/), so useApi/useApiFetch send it as a
//                    Bearer header. Short-lived (~15 min).
//   - refresh_token: httpOnly, tightly scoped to /auth so ONLY the refresh /
//                    logout server routes ever see it (never the browser JS,
//                    never the /api/** relay). Opaque + rotated every refresh.
//   - auth_mode    : marks the session 'oauth' so the client picks the OAuth
//                    refresh path (vs the first-party password fallback).
// The token endpoint returns tokens in the JSON body (no Set-Cookie), so these
// cookies are written here, not relayed.

const ACCESS_MAX_AGE = 60 * 15 // 15 minutes (matches expires_in)
const REFRESH_MAX_AGE = 60 * 60 * 24 * 90 // 90 days

export interface OAuthTokens {
  access_token: string
  refresh_token?: string
  expires_in?: number
}

// /oauth/token is an OAuth protocol endpoint: the bare RFC 6749 shape
// { access_token, ... } on success, { error, error_description } on failure.
// Tier-A contract: docs/integration/oauth/04-tokens-and-errors.md.
//
// Success is judged by the PRESENCE of access_token, never by a status field —
// that is what keeps this reader honest if the response shape ever shifts again.
export interface TokenWire {
  access_token?: string
  refresh_token?: string
  expires_in?: number
  error?: string
  error_description?: string
}

export const tokenWirePayload = (
  res: TokenWire | null | undefined
): OAuthTokens | null => (res?.access_token ? (res as OAuthTokens) : null)

export const tokenWireError = (
  res: TokenWire | null | undefined
): string | undefined => res?.error_description ?? res?.error

export const landOAuthSession = (event: H3Event, tokens: OAuthTokens) => {
  const secure = !import.meta.dev
  setCookie(event, 'access_token', tokens.access_token, {
    path: '/',
    httpOnly: false,
    sameSite: 'lax',
    secure,
    maxAge: tokens.expires_in || ACCESS_MAX_AGE
  })
  if (tokens.refresh_token) {
    setCookie(event, 'refresh_token', tokens.refresh_token, {
      path: '/auth',
      httpOnly: true,
      sameSite: 'lax',
      secure,
      maxAge: REFRESH_MAX_AGE
    })
  }
  setCookie(event, 'auth_mode', 'oauth', {
    path: '/',
    httpOnly: false,
    sameSite: 'lax',
    secure,
    maxAge: REFRESH_MAX_AGE
  })
}

export const clearOAuthSession = (event: H3Event) => {
  deleteCookie(event, 'access_token', { path: '/' })
  deleteCookie(event, 'refresh_token', { path: '/auth' })
  deleteCookie(event, 'auth_mode', { path: '/' })
}
