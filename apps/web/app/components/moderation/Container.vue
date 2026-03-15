<script setup lang="ts">
import { MODERATION_TABS } from '~/constants/admin'

const api = useApi()

const jobs = ref<ModerationJob[]>([])
const isLoading = ref(true)
const activeTab = ref('pending')

const fetchJobs = async () => {
  isLoading.value = true
  try {
    const response = await api.get<ModerationJob[]>(`/moderation/jobs?status=${activeTab.value}`)
    if (response.code === 0) {
      jobs.value = response.data
    }
  } catch (error) {
    console.error('Failed to fetch moderation jobs:', error)
  } finally {
    isLoading.value = false
  }
}

watch(activeTab, () => fetchJobs())
onMounted(() => fetchJobs())
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-bold text-gray-800 dark:text-white">内容审核</h1>
      <p class="mt-1 text-gray-600 dark:text-gray-400">审核和管理用户生成的内容</p>
    </div>

    <div class="flex gap-2 rounded-lg bg-gray-100 p-1 dark:bg-gray-800">
      <button
        v-for="tab in MODERATION_TABS"
        :key="tab.id"
        :class="[
          'flex items-center gap-2 rounded-md px-4 py-2 text-sm font-medium transition-colors',
          activeTab === tab.id
            ? 'bg-white text-gray-900 shadow-sm dark:bg-gray-700 dark:text-white'
            : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white'
        ]"
        @click="activeTab = tab.id"
      >
        <Icon :name="tab.icon" class="size-4" />
        {{ tab.label }}
      </button>
    </div>

    <div class="rounded-xl bg-white shadow-sm dark:bg-gray-800">
      <div v-if="isLoading" class="flex items-center justify-center py-12">
        <Icon name="lucide:loader-2" class="size-8 animate-spin text-indigo-500" />
      </div>

      <div v-else-if="jobs.length === 0" class="py-12 text-center">
        <Icon name="lucide:shield-check" class="mx-auto mb-4 size-12 text-gray-300" />
        <p class="text-gray-500 dark:text-gray-400">
          暂无{{ activeTab === 'pending' ? '待审核' : activeTab === 'approved' ? '已通过' : '已拒绝' }}的审核任务
        </p>
      </div>

      <div v-else class="divide-y divide-gray-200 dark:divide-gray-700">
        <ModerationJobItem v-for="job in jobs" :key="job.id" :job="job" />
      </div>
    </div>
  </div>
</template>
