<script setup lang="ts">
// Identity-handshake header for the OAuth consent page: the requesting client
// on the left, the 鲲 Galgame account mark on the right, joined by a dashed
// connector. Purely presentational.
//
// GET /oauth/client-info returns the client's logo_url (the same app-directory
// image /oauth/ecosystem serves); clients without one fall back to an
// initial-letter disc.
const props = defineProps<{
  clientName?: string
  clientLogo?: string
}>()

const initial = computed(() =>
  (props.clientName?.trim()[0] ?? '?').toUpperCase()
)
</script>

<template>
  <div class="flex items-center justify-center gap-2">
    <span
      class="border-default-200 bg-default-100 text-default-600 flex size-14 shrink-0 items-center justify-center overflow-hidden rounded-2xl border text-xl font-bold"
      :aria-label="clientName || '应用'"
    >
      <KunImage
        v-if="clientLogo"
        :src="clientLogo"
        :alt="clientName || '应用'"
        :width="56"
        :height="56"
        object-fit="cover"
        class-name="size-full"
      />
      <template v-else>{{ initial }}</template>
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
