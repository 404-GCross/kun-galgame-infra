<script setup lang="ts">
import type { Galgame, GalgameCalendarResponse } from '~/shared/types/galgame'
import { resolveBannerUrl } from '~/shared/utils/resolveImage'

// Galgame 发售月历:按 ISO 自然月翻页,已发售 + 未发售混排,日期升序。
// 数据来自公开接口 GET /galgame/calendar(精度感知,见
// docs/galgame_wiki/06-release-calendar-design.md)。

const api = useApi()
const cdnBase = useRuntimeConfig().public.imageCdnBase as string

const pad = (n: number) => String(n).padStart(2, '0')
const currentMonth = () => {
  const d = new Date()
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`
}

// URL-synced so a month is shareable / reloadable.
const month = useQueryState('month', currentMonth())
const contentLimit = useQueryState('cl', 'sfw')

const { data, status: fetchStatus } = await useAsyncData(
  'galgame-calendar',
  async () => {
    const r = await api.get<GalgameCalendarResponse>('/galgame/calendar', {
      month: month.value,
      content_limit: contentLimit.value
    })
    return r.code === 0 ? r.data : null
  },
  { watch: [month, contentLimit] }
)

// `data` retains the previous month while the next loads → no flash on nav.
const items = computed<Galgame[]>(() => data.value?.items ?? [])
const today = computed(() => data.value?.today ?? '')
const count = computed(() => data.value?.meta.count ?? 0)
const loading = computed(() => fetchStatus.value === 'pending')

const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六']
const weekday = (date: string) => WEEKDAYS[new Date(`${date}T00:00:00`).getDay()] ?? ''
const dayLabel = (date: string) => {
  const [, m, d] = date.split('-')
  return `${Number(m)} 月 ${Number(d)} 日`
}
const monthLabel = computed(() => {
  const [y, m] = month.value.split('-')
  return `${y} 年 ${Number(m)} 月`
})

// items arrive ascending by date; day-precision rows group by date, day-unknown
// (release_precision='month') rows collect into a trailing "日期待定" bucket.
const grouped = computed(() => {
  const days: { date: string; items: Galgame[] }[] = []
  const pending: Galgame[] = []
  let cur: { date: string; items: Galgame[] } | null = null
  for (const g of items.value) {
    if (g.release_precision === 'month') {
      pending.push(g)
      continue
    }
    const d = g.release_date ?? ''
    if (!cur || cur.date !== d) {
      cur = { date: d, items: [] }
      days.push(cur)
    }
    cur.items.push(g)
  }
  return { days, pending }
})

const displayName = (g: Galgame) =>
  g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw || '(无标题)'
const coverUrl = (g: Galgame) =>
  resolveBannerUrl(g, { cdnBase, variant: 'mini' })

const shiftMonth = (delta: number) => {
  const [y, m] = month.value.split('-').map(Number)
  const d = new Date(y!, m! - 1 + delta, 1)
  month.value = `${d.getFullYear()}-${pad(d.getMonth() + 1)}`
}

const CL_TABS = [
  { id: 'sfw', label: '全年龄' },
  { id: 'nsfw', label: 'R18' }
]
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-foreground text-2xl font-bold">Galgame 发售月历</h1>
      <div class="flex gap-2">
        <KunButton
          v-for="tab in CL_TABS"
          :key="tab.id"
          :variant="contentLimit === tab.id ? 'solid' : 'light'"
          color="primary"
          size="sm"
          @click="contentLimit = tab.id"
        >
          {{ tab.label }}
        </KunButton>
      </div>
    </div>

    <!-- Month navigator -->
    <div class="flex flex-wrap items-center gap-3">
      <KunButton variant="light" @click="shiftMonth(-1)">
        <KunIcon name="lucide:chevron-left" class="size-4" />
      </KunButton>
      <div class="text-foreground min-w-32 text-center text-lg font-semibold">
        {{ monthLabel }}
      </div>
      <KunButton variant="light" @click="shiftMonth(1)">
        <KunIcon name="lucide:chevron-right" class="size-4" />
      </KunButton>
      <!-- Jump (native month input emits YYYY-MM directly) -->
      <input
        v-model="month"
        type="month"
        class="border-default-200 bg-content1 text-foreground rounded-lg border px-3 py-1.5 text-sm"
        aria-label="跳转到指定月份"
      />
      <span class="text-default-500 ml-auto text-sm">
        本月 {{ count }} 部
        <KunIcon
          v-if="loading"
          name="lucide:loader-circle"
          class="ml-1 inline size-4 animate-spin"
        />
      </span>
    </div>

    <!-- Empty -->
    <KunCard
      v-if="!loading && grouped.days.length === 0 && grouped.pending.length === 0"
      class="py-16"
    >
      <div class="text-default-400 flex flex-col items-center gap-2">
        <KunIcon name="lucide:calendar-x" class="size-8" />
        <span>{{ monthLabel }}暂无发售计划</span>
      </div>
    </KunCard>

    <!-- Day groups -->
    <div
      v-for="day in grouped.days"
      :key="day.date"
      class="space-y-2"
    >
      <div class="flex items-center gap-2">
        <span
          class="text-foreground text-sm font-semibold"
          :class="{ 'text-primary': day.date === today }"
        >
          {{ dayLabel(day.date) }} (周{{ weekday(day.date) }})
        </span>
        <span
          v-if="day.date === today"
          class="bg-primary-100 text-primary rounded-full px-2 py-0.5 text-xs"
        >
          今天
        </span>
        <div class="border-default-200 h-px flex-1 border-t" />
      </div>
      <GalgameCalendarGrid :items="day.items" />
    </div>

    <!-- Day-unknown (month precision) bucket -->
    <div v-if="grouped.pending.length > 0" class="space-y-2">
      <div class="flex items-center gap-2">
        <span class="text-default-500 text-sm font-semibold">本月内 · 日期待定</span>
        <div class="border-default-200 h-px flex-1 border-t" />
      </div>
      <GalgameCalendarGrid :items="grouped.pending" />
    </div>
  </div>
</template>
