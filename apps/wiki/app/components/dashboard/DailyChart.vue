<script setup lang="ts">
import { ADMIN_STATS_ENTITIES } from '~/constants/admin'
import type { AdminStatsDaily } from '~/shared/types/galgame'

const props = defineProps<{ daily: AdminStatsDaily[] }>()

const activeKey = ref<keyof Omit<AdminStatsDaily, 'date'>>('galgame_revision')

const maxValue = computed(() => {
  if (props.daily.length === 0) return 1
  let m = 0
  for (const d of props.daily) {
    const v = d[activeKey.value] as number
    if (v > m) m = v
  }
  return m || 1
})

const formatDate = (iso: string) => {
  // YYYY-MM-DD → MM-DD
  return iso.slice(5)
}

const total = computed(() =>
  props.daily.reduce((sum, d) => sum + ((d[activeKey.value] as number) || 0), 0)
)
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap gap-2">
      <button
        v-for="entity in ADMIN_STATS_ENTITIES"
        :key="entity.key"
        class="rounded-full px-3 py-1 text-xs transition-colors"
        :class="
          activeKey === entity.key
            ? 'bg-primary text-white'
            : 'bg-content2 text-default-500 hover:bg-default-100'
        "
        @click="activeKey = entity.key"
      >
        {{ entity.label }}
      </button>
      <span class="text-default-500 ml-auto text-sm">
        合计 <span class="text-foreground font-semibold">{{ total }}</span>
      </span>
    </div>

    <div v-if="daily.length === 0" class="text-default-400 py-8 text-center">
      暂无数据
    </div>
    <div v-else class="flex h-48 items-end gap-1">
      <div
        v-for="d in daily"
        :key="d.date"
        class="group relative flex flex-1 flex-col items-center"
      >
        <div
          class="bg-primary-200 hover:bg-primary w-full rounded-t transition-colors"
          :style="{
            height: `${((d[activeKey] as number) / maxValue) * 100}%`,
            minHeight: '2px'
          }"
        />
        <span class="text-default-400 mt-1 rotate-45 text-[10px] origin-left">
          {{ formatDate(d.date) }}
        </span>
        <div
          class="bg-foreground text-background pointer-events-none absolute bottom-full mb-1 rounded px-2 py-1 text-xs opacity-0 transition-opacity group-hover:opacity-100 whitespace-nowrap z-10"
        >
          {{ d.date }}: {{ d[activeKey] }}
        </div>
      </div>
    </div>
  </div>
</template>
