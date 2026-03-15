<script setup lang="ts">
import { USER_STATUS_MAP } from '~/constants/admin'

const props = defineProps<{ status: number }>()

const badge = computed(() => USER_STATUS_MAP[props.status] ?? { label: '未知', color: 'default' as const })
</script>

<template>
  <KunBadge :color="badge.color" variant="flat" size="sm">
    {{ badge.label }}
  </KunBadge>
</template>
