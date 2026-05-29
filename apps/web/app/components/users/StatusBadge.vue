<script setup lang="ts">
import { USER_STATUS_MAP } from '~/constants/admin'

const props = defineProps<{ status: number; isAnonymized?: boolean }>()

// Anonymized (PII scrubbed) is terminal and takes precedence over the raw
// status (which is 1/banned for anonymized users).
const badge = computed(() => {
  if (props.isAnonymized) return { label: '已注销', color: 'default' as const }
  return USER_STATUS_MAP[props.status] ?? { label: '未知', color: 'default' as const }
})
</script>

<template>
  <KunChip :color="badge.color" variant="flat" size="sm">
    {{ badge.label }}
  </KunChip>
</template>
