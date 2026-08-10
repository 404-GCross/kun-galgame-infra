<script setup lang="ts">
import type { DevUsageDay } from '~~/shared/types/dev'

const props = defineProps<{ days: DevUsageDay[] }>()

const max = computed(() =>
  Math.max(1, ...props.days.map((d) => d.count))
)

const barHeight = (count: number) => `${Math.round((count / max.value) * 100)}%`

const labelAt = (i: number) => {
  const n = props.days.length
  return i === 0 || i === n - 1 || i === Math.floor((n - 1) / 2)
}

const shortDay = (day: string) => day.slice(5) // MM-DD
</script>

<template>
  <div v-if="days.length">
    <div class="flex h-48 items-end gap-1">
      <div
        v-for="d in days"
        :key="d.day"
        class="group relative flex h-full flex-1 items-end"
      >
        <div
          class="min-h-[3px] w-full rounded-t transition-colors"
          :class="
            d.count > 0
              ? 'bg-primary/80 hover:bg-primary'
              : 'bg-default-200'
          "
          :style="{ height: barHeight(d.count) }"
        />
        <div
          class="pointer-events-none absolute -top-9 left-1/2 z-10 hidden -translate-x-1/2 whitespace-nowrap rounded-md bg-foreground px-2 py-1 text-xs text-background shadow-sm group-hover:block"
        >
          <span class="font-mono">{{ d.day }}</span>
          ·
          <span class="font-semibold">{{ d.count.toLocaleString() }}</span> 次
        </div>
      </div>
    </div>
    <div class="mt-2 flex gap-1">
      <div
        v-for="(d, i) in days"
        :key="d.day"
        class="flex-1 text-center text-[10px] text-default-400"
      >
        <span v-if="labelAt(i)" class="font-mono">{{ shortDay(d.day) }}</span>
      </div>
    </div>
  </div>

  <div
    v-else
    class="flex h-48 items-center justify-center text-sm text-default-400"
  >
    暂无数据
  </div>
</template>
