// Promise-based confirm dialog backed by KunModal — replaces native
// window.confirm(). Mirrors the useKunMessage singleton pattern: one
// module-level reactive state + a single <CommonConfirmHost> mounted in
// app.vue. Only ever invoked from client user-event handlers, so the
// shared module state is never touched during SSR render (the host's
// KunModal stays closed/empty on the server — no cross-request leak).

interface KunConfirmOptions {
  title?: string
  content: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
}

const state = reactive({
  open: false,
  title: '确认',
  content: '',
  confirmText: '确定',
  cancelText: '取消',
  danger: false
})

let resolver: ((value: boolean) => void) | null = null

// Read-only-ish state accessor for the host component.
export const useKunConfirmState = () => state

// Called by the host's buttons / dismiss. Resolves the pending promise.
export const resolveKunConfirm = (value: boolean) => {
  if (!state.open) return
  state.open = false
  const r = resolver
  resolver = null
  r?.(value)
}

// Imperative API: `if (!(await useKunConfirm({ content: '...' }))) return`
export const useKunConfirm = (opts: KunConfirmOptions): Promise<boolean> => {
  // If a prior dialog is somehow still open, cancel it first.
  if (resolver) resolveKunConfirm(false)

  state.title = opts.title ?? '确认'
  state.content = opts.content
  state.confirmText = opts.confirmText ?? '确定'
  state.cancelText = opts.cancelText ?? '取消'
  state.danger = opts.danger ?? false
  state.open = true

  return new Promise<boolean>((resolve) => {
    resolver = resolve
  })
}
