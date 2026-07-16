<script setup lang="ts">
import '@scalar/api-reference/style.css'
import { galgameSpec, catalogSpec } from '~/generated/specs'

// API documentation. Scalar renders the two Tier-A public specs (bundled at
// build time — no CDN/network fetch, so a strict CSP is satisfied). One
// <KunTab> drives the active face; the Scalar instance remounts per face/theme
// via :key. The heavy component is lazy-loaded and client-only so it never
// enters the SSR module graph (Scalar touches browser globals at import).
const colorMode = useColorMode()

useSeoMeta({
  title: 'API 文档',
  description:
    'NextMoe 开放 API 交互式文档（Scalar）：galgame 面与 catalog 面两份公开规范。'
})

const ScalarApiReference = defineAsyncComponent(() =>
  import('@scalar/api-reference').then((m) => m.ApiReference)
)

const faceItems = [
  { value: 'galgame', textValue: 'Galgame 面' },
  { value: 'catalog', textValue: 'Catalog 面' }
]
const activeFace = ref('galgame')

const specContent = computed(() =>
  activeFace.value === 'galgame' ? galgameSpec : catalogSpec
)

const scalarConfig = computed(() => ({
  content: specContent.value,
  // No external hosts (strict CSP): fall back to inherited/system fonts.
  withDefaultFonts: false,
  layout: 'modern' as const,
  hideModels: false,
  // The portal owns the theme toggle; keep Scalar in lockstep, no second toggle.
  darkMode: colorMode.value === 'dark',
  hideDarkModeToggle: true
}))

// Remount Scalar when the face or theme changes (it reads these on init).
const scalarKey = computed(() => `${activeFace.value}-${colorMode.value}`)
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-bold text-foreground">API 文档</h1>
      <p class="mt-2 text-default-500">
        所有请求需带
        <code class="rounded bg-default-100 px-1.5 py-0.5 font-mono text-xs text-foreground">
          Authorization: Bearer nm_live_…
        </code>
        。按 tier 分层限流（free 60 次/分 · 50,000 次/日）,超限返回
        <code class="font-mono text-xs text-foreground">429</code>
        并携带 <code class="font-mono text-xs text-foreground">X-RateLimit-*</code> 头。
      </p>
    </div>

    <KunTab
      v-model="activeFace"
      :items="faceItems"
      variant="underlined"
      color="primary"
      size="md"
    />

    <ClientOnly>
      <div class="scalar-host overflow-x-auto rounded-xl border border-default-200">
        <ScalarApiReference :key="scalarKey" :configuration="scalarConfig" />
      </div>
      <template #fallback>
        <div class="flex items-center justify-center py-24">
          <KunIcon
            name="lucide:loader-circle"
            class="size-6 animate-spin text-default-400"
          />
        </div>
      </template>
    </ClientOnly>
  </div>
</template>

<style scoped>
/* Let Scalar own its internal scrolling / theming; just contain it. */
.scalar-host {
  min-height: 60vh;
}
</style>
