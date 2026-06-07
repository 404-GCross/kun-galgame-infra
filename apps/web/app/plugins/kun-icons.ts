import { registerKunIcons } from '@kungal/ui-core'
import { KUN_ICONS } from '~/assets/kun-icons'

// Register this app's icons into KunUI's in-memory registry so <KunIcon> renders
// them as INLINE SVG on both server and client — identical markup, so no
// hydration mismatch and no @nuxt/icon network fetch. Runs on server + client
// (default plugin). The map is generated from actual usage; regenerate with
// `pnpm --filter web icons` after adding/removing icons.
export default defineNuxtPlugin(() => {
  registerKunIcons(KUN_ICONS)
})
