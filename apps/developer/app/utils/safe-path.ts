// Open-redirect guard for post-login destinations: accept only a same-app
// path like "/dashboard?tab=keys". Validated with the WHATWG URL parser
// against a sentinel origin, so authority-smuggling variants a prefix check
// misses ("//host", "/\\host" — backslash is treated as a slash by browsers)
// are rejected by origin comparison instead of string guessing.
export const isSafeInternalPath = (r?: string | null): r is string => {
  if (!r || !r.startsWith('/')) return false
  try {
    return new URL(r, 'https://guard.internal').origin === 'https://guard.internal'
  } catch {
    return false
  }
}
