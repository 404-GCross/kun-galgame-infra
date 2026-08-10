import { registerKunIcons } from '@kungal/ui-core'
import { KUN_ICONS } from '~/assets/kun-icons'

// hydration mismatch and no @nuxt/icon network fetch (a strict CSP would block
export default defineNuxtPlugin(() => {
  registerKunIcons(KUN_ICONS)
})
