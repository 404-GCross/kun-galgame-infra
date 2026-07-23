// PKCE (RFC 7636, S256) + CSRF-state helpers for the OAuth Authorization Code
// login against our own IdP. Client-only — uses Web Crypto (crypto.subtle),
// which is unavailable during SSR, so only call these from a browser context
// (the login CTA / callback page). Mirrors the recipe in
// docs/integration/oauth/oauth-integration-guide.md §3.

const base64UrlEncode = (bytes: Uint8Array): string =>
  btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')

// A 43–128 char high-entropy string (32 random bytes → base64url).
export const generateCodeVerifier = (): string => {
  const array = new Uint8Array(32)
  crypto.getRandomValues(array)
  return base64UrlEncode(array)
}

// code_challenge = base64url(SHA-256(verifier)) for code_challenge_method=S256.
export const generateCodeChallenge = async (
  verifier: string
): Promise<string> => {
  const data = new TextEncoder().encode(verifier)
  const digest = await crypto.subtle.digest('SHA-256', data)
  return base64UrlEncode(new Uint8Array(digest))
}

// 16 random bytes as hex — the `state` param, checked on callback to block CSRF.
export const generateState = (): string => {
  const array = new Uint8Array(16)
  crypto.getRandomValues(array)
  return Array.from(array, (b) => b.toString(16).padStart(2, '0')).join('')
}
