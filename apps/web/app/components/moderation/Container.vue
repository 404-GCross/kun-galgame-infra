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
      <h1 class="text-2xl font-bold text-foreground">内容审核</h1>
      <p class="mt-1 text-default-500">审核和管理用户生成的内容</p>
    </div>

    <div class="flex gap-2">
      <KunButton
        v-for="tab in MODERATION_TABS"
        :key="tab.id"
        :variant="activeTab === tab.id ? 'solid' : 'light'"
        color="primary"
        size="sm"
        @click="activeTab = tab.id"
      >
        <Icon :name="tab.icon" class="mr-1 size-4" />
        {{ tab.label }}
      </KunButton>
    </div>

    <div class="rounded-xl bg-content1 shadow-sm">
      <div v-if="isLoading" class="flex items-center justify-center py-12">
        <Icon name="lucide:loader-2" class="size-8 animate-spin text-primary" />
      </div>

      <div v-else-if="jobs.length === 0" class="py-12 text-center">
        <Icon name="lucide:shield-check" class="mx-auto mb-4 size-12 text-default-200" />
        <p class="text-default-400">
          暂无{{ activeTab === 'pending' ? '待审核' : activeTab === 'approved' ? '已通过' : '已拒绝' }}的审核任务
        </p>
      </div>

      <div v-else class="divide-y divide-default-200">
        <ModerationJobItem v-for="job in jobs" :key="job.id" :job="job" />
      </div>
    </div>
  </div>
</template>
