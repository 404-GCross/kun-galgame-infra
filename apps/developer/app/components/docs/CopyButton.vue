<script setup lang="ts">
import { cn } from '@kungal/ui-core'
import { useKunCopy } from '@kungal/ui-vue'

// Icon-only copy affordance. KunCopy renders its `text` as a visible pill, which
// is wrong for long / multi-line values (a curl example, a full URL) — so those
// show the value in an adjacent <code>/<pre> and copy through this button, which
// wraps KunUI's own useKunCopy composable + KunButton. Flips to a check briefly.
const props = defineProps<{ text: string; label?: string }>()

const copied = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

const onCopy = async () => {
  const ok = await useKunCopy(props.text)
  if (!ok) return
  copied.value = true
  clearTimeout(timer)
  timer = setTimeout(() => {
    copied.value = false
  }, 1500)
}

onUnmounted(() => clearTimeout(timer))
</script>

<template>
  <KunButton
    variant="light"
    size="sm"
    is-icon-only
    :aria-label="label ?? '复制'"
    @click="onCopy"
  >
    <KunIcon
      :name="copied ? 'lucide:check' : 'lucide:copy'"
      :class="cn('size-4', copied ? 'text-success' : 'text-default-400')"
    />
  </KunButton>
</template>
