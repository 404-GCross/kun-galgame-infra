<script setup lang="ts">
const api = useApi()

const sites = ref<Site[]>([])
const isLoading = ref(true)
const showCreateModal = ref(false)
const editingSite = ref<Site | null>(null)

const fetchSites = async () => {
  isLoading.value = true
  try {
    const response = await api.get<Site[]>('/sites')
    if (response.code === 0) {
      sites.value = response.data
    }
  } finally {
    isLoading.value = false
  }
}

const handleCreated = () => {
  showCreateModal.value = false
  fetchSites()
}

const handleUpdated = () => {
  editingSite.value = null
  fetchSites()
}

const handleDelete = async (id: number) => {
  const response = await api.delete(`/sites/${id}`)
  if (response.code === 0) {
    fetchSites()
  }
}

onMounted(() => fetchSites())
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-foreground">站点管理</h1>
        <p class="mt-1 text-default-500">管理连接的站点和 OAuth 配置</p>
      </div>
      <KunButton color="primary" @click="showCreateModal = true">
        <Icon name="lucide:plus" class="mr-2 size-4" />
        添加站点
      </KunButton>
    </div>

    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-primary" />
    </div>

    <div v-else-if="sites.length === 0" class="rounded-xl bg-content1 py-12 text-center shadow-sm">
      <Icon name="lucide:globe" class="mx-auto mb-4 size-12 text-default-200" />
      <p class="text-default-400">暂无站点配置</p>
    </div>

    <div v-else class="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <SitesCard
        v-for="site in sites"
        :key="site.id"
        :site="site"
        @edit="editingSite = site"
        @delete="handleDelete(site.id)"
      />
    </div>

    <SitesCreateModal
      v-model="showCreateModal"
      @created="handleCreated"
    />

    <SitesEditModal
      v-if="editingSite"
      :site="editingSite"
      @close="editingSite = null"
      @updated="handleUpdated"
    />
  </div>
</template>
