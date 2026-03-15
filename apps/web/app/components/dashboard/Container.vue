<script setup lang="ts">
import { DASHBOARD_STATS } from '~/constants/dashboard'

const auth = useAuth()
const stats = ref(DASHBOARD_STATS)
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-bold text-gray-800 dark:text-white">仪表盘</h1>
      <p class="mt-1 text-gray-600 dark:text-gray-400">
        欢迎回来，{{ auth.user.value?.name }}
      </p>
    </div>

    <div class="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
      <DashboardStatCard
        v-for="stat in stats"
        :key="stat.label"
        v-bind="stat"
      />
    </div>

    <DashboardQuickActions />

    <div class="rounded-xl bg-white p-6 shadow-sm dark:bg-gray-800">
      <h2 class="mb-4 text-lg font-semibold text-gray-800 dark:text-white">
        最近活动
      </h2>
      <div class="py-8 text-center text-gray-500 dark:text-gray-400">
        <Icon name="lucide:inbox" class="mx-auto mb-2 size-12 opacity-50" />
        <p>暂无活动记录</p>
      </div>
    </div>
  </div>
</template>
