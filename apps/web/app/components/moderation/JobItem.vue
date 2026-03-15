<script setup lang="ts">
import { MODERATION_STATUS_MAP } from '~/constants/admin'

const props = defineProps<{ job: ModerationJob }>()

const statusBadge = computed(() => MODERATION_STATUS_MAP[props.job.status] ?? { label: props.job.status, color: 'default' as const })
</script>

<template>
  <div class="flex items-center justify-between p-4 hover:bg-gray-50 dark:hover:bg-gray-700">
    <div class="flex items-center gap-4">
      <div class="flex size-10 items-center justify-center rounded-lg bg-gray-100 dark:bg-gray-900">
        <Icon name="lucide:file-text" class="size-5 text-gray-500" />
      </div>
      <div>
        <p class="font-medium text-gray-800 dark:text-white">
          {{ job.content_type }} #{{ job.content_id }}
        </p>
        <p class="text-sm text-gray-500">
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
        class="rounded-lg bg-indigo-50 px-3 py-1.5 text-sm font-medium text-indigo-600 hover:bg-indigo-100 dark:bg-indigo-900 dark:text-indigo-400 dark:hover:bg-indigo-800"
      >
        审核
      </NuxtLink>
    </div>
  </div>
</template>
