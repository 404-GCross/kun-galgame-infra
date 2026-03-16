<script setup lang="ts">
const api = useApi()

const clients = ref<OAuthClient[]>([])
const isLoading = ref(true)

const fetchClients = async () => {
  isLoading.value = true
  try {
    const response = await api.get<OAuthClient[]>('/oauth/clients')
    if (response.code === 0) {
      clients.value = response.data
    }
  } catch (error) {
    console.error('Failed to fetch OAuth clients:', error)
  } finally {
    isLoading.value = false
  }
}

onMounted(() => fetchClients())
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-foreground">OAuth 客户端</h1>
        <p class="mt-1 text-default-500">管理 OAuth 2.0 客户端应用</p>
      </div>
      <KunButton color="primary">
        <Icon name="lucide:plus" class="mr-2 size-4" />
        创建客户端
      </KunButton>
    </div>

    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-primary" />
    </div>

    <div v-else-if="clients.length === 0" class="rounded-xl bg-content1 py-12 text-center shadow-sm">
      <Icon name="lucide:key" class="mx-auto mb-4 size-12 text-default-200" />
      <p class="text-default-400">暂无 OAuth 客户端配置</p>
      <p class="mt-1 text-sm text-default-300">创建客户端以启用 OAuth 认证</p>
    </div>

    <div v-else class="space-y-4">
      <OauthClientsClientCard v-for="client in clients" :key="client.id" :client="client" />
    </div>
  </div>
</template>
