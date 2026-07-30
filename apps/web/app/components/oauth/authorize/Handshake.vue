<script setup lang="ts">
// Identity-handshake header for the OAuth consent page: the requesting client
// on the left, the 鲲 Galgame account mark on the right, joined by a dashed
// connector. Purely presentational — it takes only the client's display name.
//
// The client side falls back to an initial-letter disc: GET /oauth/client-info
// returns { id, name, auto_consent, site_domain } and exposes no logo today.
const props = defineProps<{
  clientName?: string
}>()

const initial = computed(() =>
  (props.clientName?.trim()[0] ?? '?').toUpperCase()
)
</script>

<template>
  <div class="flex items-center justify-center gap-2">
    <span
      class="border-default-200 bg-default-100 text-default-600 flex size-14 shrink-0 items-center justify-center rounded-2xl border text-xl font-bold"
      :aria-label="clientName || '应用'"
    >
      {{ initial }}
    </span>

    <span class="border-default-300 w-4 border-t border-dashed" aria-hidden="true" />
    <KunIcon name="lucide:arrow-left-right" class="text-default-400 size-4 shrink-0" />
    <span class="border-default-300 w-4 border-t border-dashed" aria-hidden="true" />

    <KunImage
      src="/favicon.webp"
      alt="鲲 Galgame"
      :width="56"
      :height="56"
      object-fit="cover"
      class-name="border-default-200 size-14 shrink-0 overflow-hidden rounded-2xl border"
    />
  </div>
</template>
