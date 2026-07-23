<script setup lang="ts">
import type { DevUsageSummary } from '~~/shared/types/dev'

// Account-level usage: window totals + a daily volume chart + a per-app
// breakdown, over a selectable window (7 / 14 / 30 days). Reads GET /dev/usage;
// the URL is reactive so switching the window refetches (SSR-rendered first).
useSeoMeta({ title: '用量', robots: 'noindex' })

const days = ref(7)
const dayOptions = [7, 14, 30]

const { data } = await useApiFetch<DevUsageSummary>(
  () => `/dev/usage?days=${days.value}`
)

const summary = computed(() => data.value)
const daily = computed(() => summary.value?.daily ?? [])
const byApp = computed(() => summary.value?.by_app ?? [])

const total = computed(() => summary.value?.total_count ?? 0)
const total4xx = computed(() => summary.value?.total_4xx ?? 0)
const total5xx = computed(() => summary.value?.total_5xx ?? 0)
const errorRate = computed(() =>
  total.value > 0
    ? (((total4xx.value + total5xx.value) / total.value) * 100).toFixed(1)
    : '0.0'
)

const tiles = computed(() => [
  { label: '总请求', value: total.value.toLocaleString(), tone: 'foreground' },
  { label: '错误率', value: `${errorRate.value}%`, tone: 'foreground' },
  {
    label: '4xx',
    value: total4xx.value.toLocaleString(),
    tone: total4xx.value > 0 ? 'warning' : 'default'
  },
  {
    label: '5xx',
    value: total5xx.value.toLocaleString(),
    tone: total5xx.value > 0 ? 'danger' : 'default'
  }
])

const toneClass: Record<string, string> = {
  foreground: 'text-foreground',
  warning: 'text-warning',
  danger: 'text-danger',
  default: 'text-default-400'
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-2xl font-bold text-foreground">用量</h1>
        <p class="mt-1 text-sm text-default-500">
          你名下所有应用的调用量汇总（按 UTC 日）。
        </p>
      </div>
      <div class="flex rounded-lg border border-default-200 p-0.5">
        <button
          v-for="opt in dayOptions"
          :key="opt"
          type="button"
          class="rounded-md px-3 py-1 text-sm font-medium transition-colors"
          :class="
            days === opt
              ? 'bg-primary-50 text-primary'
              : 'text-default-500 hover:text-foreground'
          "
          @click="days = opt"
        >
          {{ opt }} 天
        </button>
      </div>
    </div>

    <!-- Window totals -->
    <div class="grid grid-cols-2 gap-4 md:grid-cols-4">
      <div
        v-for="tile in tiles"
        :key="tile.label"
        class="rounded-xl border border-default-200 bg-content1 px-5 py-4"
      >
        <p class="text-xs text-default-400">{{ tile.label }}</p>
        <p
          class="mt-1 text-2xl font-bold"
          :class="toneClass[tile.tone]"
        >
          {{ tile.value }}
        </p>
      </div>
    </div>

    <!-- Daily volume -->
    <KunCard content-class="justify-start gap-0 items-stretch" class-name="p-6">
      <div class="mb-5 flex items-center justify-between">
        <h2 class="text-base font-semibold text-foreground">每日调用量</h2>
        <span class="text-xs text-default-400">最近 {{ days }} 天</span>
      </div>
      <UsageChart :days="daily" />
    </KunCard>

    <!-- Per-app breakdown -->
    <div>
      <h2 class="mb-3 text-lg font-semibold text-foreground">按应用</h2>
      <KunCard
        v-if="byApp.length"
        content-class="p-0"
        class-name="overflow-hidden"
      >
        <div class="overflow-x-auto">
          <table class="w-full min-w-[32rem] text-sm">
            <thead>
              <tr
                class="border-b border-default-200 text-left text-default-400"
              >
                <th class="px-4 py-2 font-medium">应用</th>
                <th class="px-4 py-2 text-right font-medium">请求数</th>
                <th class="px-4 py-2 text-right font-medium">4xx</th>
                <th class="px-4 py-2 text-right font-medium">5xx</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="(row, i) in byApp"
                :key="row.client_id"
                class="border-b border-default-100"
                :class="i === byApp.length - 1 && 'border-b-0'"
              >
                <td class="px-4 py-2">
                  <NuxtLink
                    :to="`/apps/${row.client_id}`"
                    class="font-medium text-foreground hover:text-primary hover:underline"
                  >
                    {{ row.name }}
                  </NuxtLink>
                </td>
                <td
                  class="px-4 py-2 text-right font-medium text-foreground"
                >
                  {{ row.count.toLocaleString() }}
                </td>
                <td
                  class="px-4 py-2 text-right"
                  :class="
                    row.status_4xx > 0 ? 'text-warning' : 'text-default-400'
                  "
                >
                  {{ row.status_4xx.toLocaleString() }}
                </td>
                <td
                  class="px-4 py-2 text-right"
                  :class="
                    row.status_5xx > 0 ? 'text-danger' : 'text-default-400'
                  "
                >
                  {{ row.status_5xx.toLocaleString() }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </KunCard>

      <KunCard v-else content-class="p-10">
        <p class="text-center text-default-400">
          还没有应用。前往
          <NuxtLink to="/dashboard" class="text-primary hover:underline">
            控制台
          </NuxtLink>
          创建一个即可开始调用。
        </p>
      </KunCard>
    </div>

    <p class="text-xs text-default-400">
      免费层配额:每密钥 60 次/分 · 50,000 次/日。用量按 UTC
      日累计,计量周期性落库,最新数据可能有几分钟延迟。
    </p>
  </div>
</template>
