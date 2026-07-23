// Global login-modal state. The portal has a SINGLE login UI — one modal
// rendered once in app.vue (LoginModal). The header CTA, the landing CTA, and
// the /login fallback route (guarded-route + 401 redirects) all open THIS.
//
// `redirect` carries a post-login destination for guarded deep links; a login
// opened from a plain button leaves it null and the modal lands on /dashboard.
// Only same-origin paths are accepted (open-redirect guard).
export const useLoginModal = () => {
  const isOpen = useState<boolean>('login-modal-open', () => false)
  const redirect = useState<string | null>('login-modal-redirect', () => null)

  const open = (to?: string | null) => {
    redirect.value =
      to && to.startsWith('/') && !to.startsWith('//') ? to : null
    isOpen.value = true
  }

  const close = () => {
    isOpen.value = false
  }

  return { isOpen, redirect, open, close }
}
