<script setup lang="ts">
const props = defineProps<{
  page: number
  total: number
  limit: number
}>()

const emit = defineEmits<{
  'update:page': [value: number]
}>()

const totalPages = computed(() =>
  Math.max(1, Math.ceil(props.total / props.limit))
)

const go = (delta: number) => {
  const next = props.page + delta
  if (next < 1 || next > totalPages.value) return
  emit('update:page', next)
}
</script>

<template>
  <div class="flex items-center justify-between">
    <span class="text-default-500 text-sm">
      第 {{ page }} / {{ totalPages }} 页 · 共 {{ total }} 条
    </span>
    <div class="flex gap-2">
      <KunButton variant="light" :disabled="page <= 1" @click="go(-1)">
        上一页
      </KunButton>
      <KunButton
        variant="light"
        :disabled="page >= totalPages"
        @click="go(1)"
      >
        下一页
      </KunButton>
    </div>
  </div>
</template>
