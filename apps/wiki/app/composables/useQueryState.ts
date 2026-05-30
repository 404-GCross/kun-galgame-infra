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
//
// The return type WIDENS the literal default to its base type, so a call like
// `useQueryState('page', 1)` is `Ref<number>` (not `Ref<1>`) and
// `useQueryState('tab', 'all')` is `Ref<string>` (not `Ref<'all'>`). Without
// this, the inferred literal type would reject normal reassignment
// (`page.value = 2`) and cross-value comparison (`tab.value === 'x'`).
type WidenQueryState<T> = T extends number ? number : string

export const useQueryState = <T extends string | number>(
  key: string,
  defaultValue: T
): Ref<WidenQueryState<T>> => {
  const route = useRoute()
  const router = useRouter()

  const parseFromUrl = (raw: unknown): WidenQueryState<T> => {
    if (raw === undefined || raw === null || raw === '')
      return defaultValue as WidenQueryState<T>
    const str = Array.isArray(raw) ? (raw[0] ?? '') : String(raw)
    if (typeof defaultValue === 'number') {
      const n = Number(str)
      return (Number.isFinite(n) ? n : defaultValue) as WidenQueryState<T>
    }
    return str as WidenQueryState<T>
  }

  const state = ref(parseFromUrl(route.query[key])) as Ref<WidenQueryState<T>>

  watch(state, (value) => {
    // Omit the key via rest-destructure rather than `delete next[key]`
    // (dynamic delete is lint-flagged and deopts the object shape).
    const { [key]: _omit, ...rest } = route.query
    const next =
      value === defaultValue || value === ''
        ? rest
        : { ...rest, [key]: String(value) }
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
