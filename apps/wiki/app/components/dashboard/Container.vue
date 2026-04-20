<script setup lang="ts">
import { ADMIN_STATS_ENTITIES } from '~/constants/admin'
import type { AdminStatsResponse } from '~/shared/types/galgame'

const api = useApi()

const days = ref(30)
const stats = ref<AdminStatsResponse | null>(null)
const loading = ref(false)

const loadStats = async () => {
  loading.value = true
  try {
    const response = await api.get<AdminStatsResponse>('/admin/stats', {
      days: days.value
    })
    if (response.code === 0) {
      stats.value = response.data
    }
  } finally {
    loading.value = false
  }
}

watch(days, loadStats, { immediate: true })

const totalsList = computed(() => {
  if (!stats.value) return []
  return ADMIN_STATS_ENTITIES.map((e) => ({
    ...e,
    value: stats.value!.totals[e.key] ?? 0
  }))
})

const dayOptions = [
  { value: 7, label: '近 7 天' },
  { value: 30, label: '近 30 天' },
  { value: 90, label: '近 90 天' }
]
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <h1 class="text-foreground text-2xl font-bold">仪表盘</h1>
      <div class="flex gap-2">
        <button
          v-for="opt in dayOptions"
          :key="opt.value"
          class="rounded-lg px-3 py-1.5 text-sm transition-colors"
          :class="
            days === opt.value
              ? 'bg-primary text-white'
              : 'bg-content2 text-default-500 hover:bg-default-100'
          "
          @click="days = opt.value"
        >
          {{ opt.label }}
        </button>
      </div>
    </div>

    <div
      v-if="loading && !stats"
      class="text-default-400 flex items-center justify-center py-20"
    >
      <Icon name="lucide:loader-2" class="size-6 animate-spin" />
    </div>

    <template v-else-if="stats">
      <div
        class="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-7"
      >
        <KunCard
          v-for="item in totalsList"
          :key="item.key"
          class="p-4"
        >
          <div class="flex items-center gap-3">
            <div class="bg-default-100 rounded-lg p-2" :class="item.color">
              <Icon :name="item.icon" class="size-6" />
            </div>
            <div class="min-w-0">
              <p class="text-default-500 truncate text-xs">{{ item.label }}</p>
              <p class="text-foreground text-xl font-bold">
                {{ item.value.toLocaleString() }}
              </p>
            </div>
          </div>
        </KunCard>
      </div>

      <KunCard class="p-6">
        <h2 class="text-foreground mb-4 text-lg font-semibold">
          每日新增（近 {{ days }} 天）
        </h2>
        <DashboardDailyChart :daily="stats.daily" />
      </KunCard>
    </template>
  </div>
</template>
