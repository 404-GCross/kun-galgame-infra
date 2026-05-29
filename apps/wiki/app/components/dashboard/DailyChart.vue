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
      <KunButton
        v-for="entity in ADMIN_STATS_ENTITIES"
        :key="entity.key"
        :variant="activeKey === entity.key ? 'solid' : 'light'"
        color="primary"
        size="xs"
        rounded="full"
        @click="activeKey = entity.key"
      >
        {{ entity.label }}
      </KunButton>
      <span class="text-default-500 ml-auto text-sm">
        合计 <span class="text-foreground font-semibold">{{ total }}</span>
      </span>
    </div>

    <div v-if="daily.length === 0" class="text-default-400 py-8 text-center">
      暂无数据
    </div>
    <!-- Horizontal scroll on small screens: with ~30-90 days, cramming every
         bar + rotated date label into a phone width overflowed the page. A
         min-width inner track keeps bars/labels legible and scrolls inside
         the card instead. Bars and labels live in two aligned bands so the
         rotated labels occupy their own fixed-height row (no spill past the
         chart edge). -->
    <div v-else class="overflow-x-auto">
      <div class="min-w-[42rem]">
        <!-- Bars band -->
        <div class="flex h-48 gap-1">
          <div
            v-for="d in daily"
            :key="d.date"
            class="group relative flex-1"
          >
            <div
              class="bg-primary-200 hover:bg-primary absolute bottom-0 w-full rounded-t transition-colors"
              :style="{
                height: `${((d[activeKey] as number) / maxValue) * 100}%`,
                minHeight: '2px'
              }"
            />
            <div
              class="bg-foreground text-background pointer-events-none absolute bottom-full left-1/2 z-10 mb-1 -translate-x-1/2 rounded px-2 py-1 text-xs whitespace-nowrap opacity-0 transition-opacity group-hover:opacity-100"
            >
              {{ d.date }}: {{ d[activeKey] }}
            </div>
          </div>
        </div>
        <!-- Labels band (rotated dates, contained in a fixed-height row) -->
        <div class="mt-1 flex h-10 gap-1">
          <div v-for="d in daily" :key="`label-${d.date}`" class="flex-1">
            <span
              class="text-default-400 inline-block origin-top-left rotate-45 text-[10px] whitespace-nowrap"
            >
              {{ formatDate(d.date) }}
            </span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
