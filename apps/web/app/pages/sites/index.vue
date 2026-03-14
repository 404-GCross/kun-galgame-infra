<script setup lang="ts">
definePageMeta({
  middleware: ['auth', 'admin'],
})

interface Site {
  id: number
  uuid: string
  name: string
  domain: string
  description: string
  created_at: string
}

const api = useApi()

const sites = ref<Site[]>([])
const isLoading = ref(true)

const fetchSites = async () => {
  isLoading.value = true
  try {
    const response = await api.get<Site[]>('/sites')
    if (response.code === 0) {
      sites.value = response.data
    }
  } catch (error) {
    console.error('Failed to fetch sites:', error)
  } finally {
    isLoading.value = false
  }
}

onMounted(() => {
  fetchSites()
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-gray-800 dark:text-white">
          Sites
        </h1>
        <p class="mt-1 text-gray-600 dark:text-gray-400">
          Manage connected sites and OAuth configurations
        </p>
      </div>
      <KunButtonButton color="primary">
        <Icon name="lucide:plus" class="mr-2 size-4" />
        Add Site
      </KunButtonButton>
    </div>

    <!-- Sites Grid -->
    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-indigo-500" />
    </div>

    <div v-else-if="sites.length === 0" class="rounded-xl bg-white py-12 text-center shadow-sm dark:bg-gray-800">
      <Icon name="lucide:globe" class="mx-auto mb-4 size-12 text-gray-300" />
      <p class="text-gray-500 dark:text-gray-400">No sites configured</p>
    </div>

    <div v-else class="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="site in sites"
        :key="site.id"
        class="rounded-xl bg-white p-6 shadow-sm transition-shadow hover:shadow-md dark:bg-gray-800"
      >
        <div class="mb-4 flex items-start justify-between">
          <div class="flex size-12 items-center justify-center rounded-lg bg-indigo-100 dark:bg-indigo-900">
            <Icon name="lucide:globe" class="size-6 text-indigo-600 dark:text-indigo-400" />
          </div>
          <button class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-700">
            <Icon name="lucide:more-vertical" class="size-5" />
          </button>
        </div>

        <h3 class="text-lg font-semibold text-gray-800 dark:text-white">
          {{ site.name }}
        </h3>
        <p class="mt-1 text-sm text-indigo-600">
          {{ site.domain }}
        </p>
        <p class="mt-2 line-clamp-2 text-sm text-gray-600 dark:text-gray-400">
          {{ site.description }}
        </p>

        <div class="mt-4 flex items-center justify-between border-t pt-4 dark:border-gray-700">
          <span class="text-xs text-gray-500">
            Created {{ new Date(site.created_at).toLocaleDateString() }}
          </span>
          <NuxtLink
            :to="`/sites/${site.id}`"
            class="text-sm font-medium text-indigo-600 hover:underline"
          >
            Configure
          </NuxtLink>
        </div>
      </div>
    </div>
  </div>
</template>
