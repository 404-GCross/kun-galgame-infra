// Binds a reactive ref to a URL query param on the current route.
// - Reads initial value from the URL on setup (so reloading / sharing a URL
//   restores list tab / page / search etc.).
// - Writes back to the URL via `router.replace` when the ref changes, so
//   back/forward history isn't polluted by every keystroke.
// - When the value equals the default (or empty string), the key is removed
//   from the URL entirely to keep it clean.
//
// Kept intentionally narrow — only supports string | number. For booleans or
// enums, pick a canonical string representation at the call site.
export const useQueryState = <T extends string | number>(
  key: string,
  defaultValue: T
): Ref<T> => {
  const route = useRoute()
  const router = useRouter()

  const parseFromUrl = (raw: unknown): T => {
    if (raw === undefined || raw === null || raw === '') return defaultValue
    const str = Array.isArray(raw) ? (raw[0] ?? '') : String(raw)
    if (typeof defaultValue === 'number') {
      const n = Number(str)
      return (Number.isFinite(n) ? (n as T) : defaultValue)
    }
    return str as T
  }

  const state = ref(parseFromUrl(route.query[key])) as Ref<T>

  watch(state, (value) => {
    const next = { ...route.query }
    if (value === defaultValue || value === '') {
      delete next[key]
    } else {
      next[key] = String(value)
    }
    router.replace({ query: next })
  })

  // Keep in sync when the route changes externally (back/forward button, etc.)
  watch(
    () => route.query[key],
    (raw) => {
      const parsed = parseFromUrl(raw)
      if (parsed !== state.value) {
        state.value = parsed
      }
    }
  )

  return state
}
