<script setup lang="ts">
import { cn } from '@kungal/ui-core'
import { useKunCopy } from '@kungal/ui-vue'

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
