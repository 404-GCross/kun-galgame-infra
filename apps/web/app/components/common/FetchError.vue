<script setup lang="ts">
// Shared fetch-error banner for admin list reads: distinguishes a failed load
// (backend down / 500 / 403) from a genuinely-empty result, so an unreachable
// service never masquerades as "nothing here". Pairs with useApiFetch's `error`.
defineProps<{ message?: string }>()
const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <div
    class="border-danger-200 bg-danger-50 flex items-center gap-3 rounded-xl border p-4"
  >
    <KunIcon name="lucide:circle-alert" class="text-danger size-5 shrink-0" />
    <p class="text-danger-700 flex-1 text-sm">
      {{ message || '加载失败，可能是服务暂时不可用，请稍后重试。' }}
    </p>
    <KunButton color="danger" variant="flat" size="sm" @click="emit('retry')">
      重试
    </KunButton>
  </div>
</template>
