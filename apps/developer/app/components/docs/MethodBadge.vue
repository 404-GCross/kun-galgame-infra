<script setup lang="ts">
import { cn } from '@kungal/ui-core'
import { DOCS_METHOD_BADGE } from '~/constants/docs'
import type { DocsMethod } from '~~/shared/types/docs'

// Compact uppercase HTTP-method pill; color carries the semantic (GET=success,
// POST=primary …). Two sizes: sm for the sidebar/list rows, md for headers.
const props = withDefaults(
  defineProps<{ method: DocsMethod; size?: 'sm' | 'md' }>(),
  { size: 'sm' }
)

const badgeClass = computed(() => DOCS_METHOD_BADGE[props.method])
const sizeClass = computed(() =>
  props.size === 'md'
    ? 'min-w-14 px-2 py-1 text-xs'
    : 'min-w-11 px-1.5 py-0.5 text-[0.625rem]'
)
</script>

<template>
  <span
    :class="
      cn(
        'inline-flex shrink-0 items-center justify-center rounded font-mono font-bold uppercase tracking-wide',
        sizeClass,
        badgeClass
      )
    "
  >
    {{ method }}
  </span>
</template>
