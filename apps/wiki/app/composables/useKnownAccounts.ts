// Local "known accounts" cache for the multi-account switcher.
//
// See docs/integration/oauth/09-account-switching.md §3.6.
//
// Wiki is a DOWNSTREAM OAuth client, cross-origin to the OP. It cannot read
// the OP's server-side "session bag" (the cross-site cookie is not sent on a
// cross-origin fetch), so the switcher's account list is a best-effort LOCAL
// cache: every account this browser has successfully signed in as / switched
// to on wiki is remembered here. The list is NOT authoritative — the OP bag is
// the source of truth, and the actual switch always re-runs the authorize
// redirect. An account that was logged out elsewhere stays in this cache until
// the next switch attempt gracefully falls back to login.
//
// SECURITY: this stores identity only (sub / name / avatar / email) — NEVER any
// token. Tokens live in the wiki-scoped httpOnly-ish cookies (see useAuth.ts).

const STORAGE_KEY = 'kg_known_accounts'

export interface KnownAccount {
  sub: string
  name: string
  avatar: string
  avatar_image_hash?: string | null
  email: string
}

const read = (): KnownAccount[] => {
  if (!import.meta.client) return []
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? (parsed as KnownAccount[]) : []
  } catch {
    // Corrupt / unparseable cache — treat as empty rather than throwing.
    return []
  }
}

const write = (accounts: KnownAccount[]) => {
  if (!import.meta.client) return
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(accounts))
  } catch {
    // Quota / private-mode failures are non-fatal: the switcher just won't
    // remember this account across reloads.
  }
}

export const useKnownAccounts = () => {
  // Reactive mirror so the submenu re-renders when the cache changes within
  // the same session. Hydrated lazily on the client (SSR-safe — empty on the
  // server pass, then filled on mount).
  const accounts = useState<KnownAccount[]>('kg-known-accounts', () => [])

  const sync = () => {
    accounts.value = read()
  }

  // Insert or update an account by `sub`. The most-recently-seen account is
  // moved to the front so the submenu shows recent accounts first.
  const upsert = (account: KnownAccount) => {
    if (!account.sub) return
    const next = read().filter((a) => a.sub !== account.sub)
    next.unshift(account)
    write(next)
    accounts.value = next
  }

  const remove = (sub: string) => {
    const next = read().filter((a) => a.sub !== sub)
    write(next)
    accounts.value = next
  }

  const list = () => accounts.value

  return { accounts, list, upsert, remove, sync }
}
