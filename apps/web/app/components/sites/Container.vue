<script setup lang="ts">
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

onMounted(() => fetchSites())
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-gray-800 dark:text-white">站点管理</h1>
        <p class="mt-1 text-gray-600 dark:text-gray-400">管理连接的站点和 OAuth 配置</p>
      </div>
      <KunButton color="primary">
        <Icon name="lucide:plus" class="mr-2 size-4" />
        添加站点
      </KunButton>
    </div>

    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-indigo-500" />
    </div>

    <div v-else-if="sites.length === 0" class="rounded-xl bg-white py-12 text-center shadow-sm dark:bg-gray-800">
      <Icon name="lucide:globe" class="mx-auto mb-4 size-12 text-gray-300" />
      <p class="text-gray-500 dark:text-gray-400">暂无站点配置</p>
    </div>

    <div v-else class="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <SitesCard v-for="site in sites" :key="site.id" :site="site" />
    </div>
  </div>
</template>
