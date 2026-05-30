<script setup lang="ts">
const api = useApi()

// SSR-rendered list (kungal-style): useApiFetch runs on the server so the
// sites are in the first-paint HTML. refresh() re-fetches client-side after
// a create / edit / delete.
const { data: sitesData, status, refresh } = await useApiFetch<Site[]>('/sites')
const sites = computed(() => sitesData.value ?? [])
const isLoading = computed(() => status.value === 'pending')

const showCreateModal = ref(false)
const editingSite = ref<Site | null>(null)

const handleCreated = () => {
  showCreateModal.value = false
  refresh()
}

const handleUpdated = () => {
  editingSite.value = null
  refresh()
}

const handleDelete = async (id: number) => {
  const response = await api.delete(`/sites/${id}`)
  if (response.code === 0) {
    useKunMessage('站点已删除', 'success')
    refresh()
  } else {
    useKunMessage(response.message || '删除失败', 'error')
  }
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <h1 class="text-2xl font-bold text-foreground">站点管理</h1>
        <p class="mt-1 text-default-500">管理连接的站点和 OAuth 配置</p>
      </div>
      <KunButton color="primary" class-name="shrink-0 self-start" @click="showCreateModal = true">
        <Icon name="lucide:plus" class="mr-2 size-4" />
        添加站点
      </KunButton>
    </div>

    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-primary" />
    </div>

    <KunCard v-else-if="sites.length === 0" content-class="justify-start gap-0" class-name="py-12 text-center">
      <Icon name="lucide:globe" class="mx-auto mb-4 size-12 text-default-200" />
      <p class="text-default-400">暂无站点配置</p>
    </KunCard>

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
