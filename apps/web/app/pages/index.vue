<script setup lang="ts">
definePageMeta({
  middleware: ['auth', 'admin']
})

const auth = useAuth()

const stats = ref([
  {
    label: '用户总数',
    value: '0',
    icon: 'lucide:users',
    color: 'bg-blue-500'
  },
  {
    label: '活跃会话',
    value: '0',
    icon: 'lucide:activity',
    color: 'bg-green-500'
  },
  { label: '站点数量', value: '3', icon: 'lucide:globe', color: 'bg-purple-500' },
  {
    label: 'OAuth 客户端',
    value: '0',
    icon: 'lucide:key',
    color: 'bg-orange-500'
  }
])
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-bold text-gray-800 dark:text-white">仪表盘</h1>
      <p class="mt-1 text-gray-600 dark:text-gray-400">
        欢迎回来，{{ auth.user.value?.name }}
      </p>
    </div>

    <!-- Stats Grid -->
    <div class="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
      <div
        v-for="stat in stats"
        :key="stat.label"
        class="rounded-xl bg-white p-6 shadow-sm dark:bg-gray-800"
      >
        <div class="flex items-center gap-4">
          <div
            :class="[
              stat.color,
              'flex size-12 items-center justify-center rounded-lg'
            ]"
          >
            <Icon :name="stat.icon" class="size-6 text-white" />
          </div>
          <div>
            <p class="text-sm text-gray-600 dark:text-gray-400">
              {{ stat.label }}
            </p>
            <p class="text-2xl font-bold text-gray-800 dark:text-white">
              {{ stat.value }}
            </p>
          </div>
        </div>
      </div>
    </div>

    <!-- Quick Actions -->
    <div class="rounded-xl bg-white p-6 shadow-sm dark:bg-gray-800">
      <h2 class="mb-4 text-lg font-semibold text-gray-800 dark:text-white">
        快捷操作
      </h2>
      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <NuxtLink
          to="/users"
          class="flex items-center gap-3 rounded-lg border border-gray-200 p-4 transition-colors hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-700"
        >
          <Icon name="lucide:user-plus" class="size-5 text-blue-500" />
          <span class="text-gray-700 dark:text-gray-300">用户管理</span>
        </NuxtLink>

        <NuxtLink
          to="/sites"
          class="flex items-center gap-3 rounded-lg border border-gray-200 p-4 transition-colors hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-700"
        >
          <Icon name="lucide:settings" class="size-5 text-purple-500" />
          <span class="text-gray-700 dark:text-gray-300">站点设置</span>
        </NuxtLink>

        <NuxtLink
          to="/oauth-clients"
          class="flex items-center gap-3 rounded-lg border border-gray-200 p-4 transition-colors hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-700"
        >
          <Icon name="lucide:key" class="size-5 text-orange-500" />
          <span class="text-gray-700 dark:text-gray-300">OAuth 客户端</span>
        </NuxtLink>

        <NuxtLink
          to="/moderation"
          class="flex items-center gap-3 rounded-lg border border-gray-200 p-4 transition-colors hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-700"
        >
          <Icon name="lucide:shield" class="size-5 text-green-500" />
          <span class="text-gray-700 dark:text-gray-300">内容审核</span>
        </NuxtLink>
      </div>
    </div>

    <!-- Recent Activity -->
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
