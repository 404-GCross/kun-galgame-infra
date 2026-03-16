<script setup lang="ts">
import { MODERATION_STATUS_MAP } from '~/constants/admin'

const props = defineProps<{ job: ModerationJob }>()

const statusBadge = computed(() => MODERATION_STATUS_MAP[props.job.status] ?? { label: props.job.status, color: 'default' as const })
</script>

<template>
  <div class="flex items-center justify-between p-4 hover:bg-default-100">
    <div class="flex items-center gap-4">
      <div class="flex size-10 items-center justify-center rounded-lg bg-default-100">
        <Icon name="lucide:file-text" class="size-5 text-default-400" />
      </div>
      <div>
        <p class="font-medium text-foreground">
          {{ job.content_type }} #{{ job.content_id }}
        </p>
        <p class="text-sm text-default-400">
          {{ new Date(job.created_at).toLocaleString() }}
        </p>
      </div>
    </div>

    <div class="flex items-center gap-3">
      <KunBadge :color="statusBadge.color" variant="flat" size="sm">
        {{ statusBadge.label }}
      </KunBadge>
      <NuxtLink
        :to="`/moderation/${job.id}`"
        class="rounded-lg bg-primary-50 px-3 py-1.5 text-sm font-medium text-primary hover:bg-primary-100"
      >
        审核
      </NuxtLink>
    </div>
  </div>
</template>
