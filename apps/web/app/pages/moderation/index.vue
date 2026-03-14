<script setup lang="ts">
definePageMeta({
  middleware: ['auth', 'admin'],
})

interface ModerationJob {
  id: number
  uuid: string
  content_type: string
  content_id: number
  status: string
  created_at: string
}

const api = useApi()

const jobs = ref<ModerationJob[]>([])
const isLoading = ref(true)
const activeTab = ref('pending')

const tabs = [
  { id: 'pending', label: 'Pending', icon: 'lucide:clock' },
  { id: 'approved', label: 'Approved', icon: 'lucide:check' },
  { id: 'rejected', label: 'Rejected', icon: 'lucide:x' },
]

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

const getStatusColor = (status: string) => {
  switch (status) {
    case 'pending':
      return 'bg-yellow-100 text-yellow-800'
    case 'approved':
      return 'bg-green-100 text-green-800'
    case 'rejected':
      return 'bg-red-100 text-red-800'
    default:
      return 'bg-gray-100 text-gray-800'
  }
}

watch(activeTab, () => {
  fetchJobs()
})

onMounted(() => {
  fetchJobs()
})
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-bold text-gray-800 dark:text-white">
        Content Moderation
      </h1>
      <p class="mt-1 text-gray-600 dark:text-gray-400">
        Review and moderate user-generated content
      </p>
    </div>

    <!-- Tabs -->
    <div class="flex gap-2 rounded-lg bg-gray-100 p-1 dark:bg-gray-800">
      <button
        v-for="tab in tabs"
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

    <!-- Jobs List -->
    <div class="rounded-xl bg-white shadow-sm dark:bg-gray-800">
      <div v-if="isLoading" class="flex items-center justify-center py-12">
        <Icon name="lucide:loader-2" class="size-8 animate-spin text-indigo-500" />
      </div>

      <div v-else-if="jobs.length === 0" class="py-12 text-center">
        <Icon name="lucide:shield-check" class="mx-auto mb-4 size-12 text-gray-300" />
        <p class="text-gray-500 dark:text-gray-400">
          No {{ activeTab }} moderation jobs
        </p>
      </div>

      <div v-else class="divide-y divide-gray-200 dark:divide-gray-700">
        <div
          v-for="job in jobs"
          :key="job.id"
          class="flex items-center justify-between p-4 hover:bg-gray-50 dark:hover:bg-gray-700"
        >
          <div class="flex items-center gap-4">
            <div class="flex size-10 items-center justify-center rounded-lg bg-gray-100 dark:bg-gray-900">
              <Icon name="lucide:file-text" class="size-5 text-gray-500" />
            </div>
            <div>
              <p class="font-medium text-gray-800 dark:text-white">
                {{ job.content_type }} #{{ job.content_id }}
              </p>
              <p class="text-sm text-gray-500">
                {{ new Date(job.created_at).toLocaleString() }}
              </p>
            </div>
          </div>

          <div class="flex items-center gap-3">
            <span
              :class="[
                getStatusColor(job.status),
                'rounded-full px-2 py-1 text-xs font-medium capitalize'
              ]"
            >
              {{ job.status }}
            </span>
            <NuxtLink
              :to="`/moderation/${job.id}`"
              class="rounded-lg bg-indigo-50 px-3 py-1.5 text-sm font-medium text-indigo-600 hover:bg-indigo-100 dark:bg-indigo-900 dark:text-indigo-400 dark:hover:bg-indigo-800"
            >
              Review
            </NuxtLink>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
