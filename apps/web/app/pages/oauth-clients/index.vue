<script setup lang="ts">
interface OAuthClient {
  id: number
  uuid: string
  site_id: number
  client_id: string
  name: string
  redirect_uri: string
  scopes: string[]
  is_active: boolean
  created_at: string
}

const api = useApi()

const clients = ref<OAuthClient[]>([])
const isLoading = ref(true)
const showCreateModal = ref(false)

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

const copyToClipboard = (text: string) => {
  navigator.clipboard.writeText(text)
}

onMounted(() => {
  fetchClients()
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-gray-800 dark:text-white">
          OAuth Clients
        </h1>
        <p class="mt-1 text-gray-600 dark:text-gray-400">
          Manage OAuth 2.0 client applications
        </p>
      </div>
      <KunButtonButton color="primary" @click="showCreateModal = true">
        <Icon name="lucide:plus" class="mr-2 size-4" />
        Create Client
      </KunButtonButton>
    </div>

    <!-- Clients List -->
    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-indigo-500" />
    </div>

    <div v-else-if="clients.length === 0" class="rounded-xl bg-white py-12 text-center shadow-sm dark:bg-gray-800">
      <Icon name="lucide:key" class="mx-auto mb-4 size-12 text-gray-300" />
      <p class="text-gray-500 dark:text-gray-400">No OAuth clients configured</p>
      <p class="mt-1 text-sm text-gray-400">
        Create a client to enable OAuth authentication
      </p>
    </div>

    <div v-else class="space-y-4">
      <div
        v-for="client in clients"
        :key="client.id"
        class="rounded-xl bg-white p-6 shadow-sm dark:bg-gray-800"
      >
        <div class="flex items-start justify-between">
          <div class="flex items-center gap-4">
            <div class="flex size-12 items-center justify-center rounded-lg bg-orange-100 dark:bg-orange-900">
              <Icon name="lucide:key" class="size-6 text-orange-600 dark:text-orange-400" />
            </div>
            <div>
              <h3 class="text-lg font-semibold text-gray-800 dark:text-white">
                {{ client.name }}
              </h3>
              <span
                :class="[
                  client.is_active
                    ? 'bg-green-100 text-green-800'
                    : 'bg-gray-100 text-gray-800',
                  'mt-1 inline-flex rounded-full px-2 py-0.5 text-xs font-medium'
                ]"
              >
                {{ client.is_active ? 'Active' : 'Inactive' }}
              </span>
            </div>
          </div>
          <button class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-700">
            <Icon name="lucide:more-vertical" class="size-5" />
          </button>
        </div>

        <div class="mt-4 space-y-3">
          <div class="flex items-center justify-between rounded-lg bg-gray-50 p-3 dark:bg-gray-900">
            <div>
              <p class="text-xs text-gray-500">Client ID</p>
              <p class="font-mono text-sm text-gray-800 dark:text-gray-200">
                {{ client.client_id }}
              </p>
            </div>
            <button
              class="rounded p-1 text-gray-400 hover:text-gray-600"
              @click="copyToClipboard(client.client_id)"
            >
              <Icon name="lucide:copy" class="size-4" />
            </button>
          </div>

          <div>
            <p class="text-xs text-gray-500">Redirect URI</p>
            <p class="text-sm text-gray-800 dark:text-gray-200">
              {{ client.redirect_uri }}
            </p>
          </div>

          <div>
            <p class="text-xs text-gray-500">Scopes</p>
            <div class="mt-1 flex flex-wrap gap-1">
              <span
                v-for="scope in client.scopes"
                :key="scope"
                class="rounded bg-indigo-100 px-2 py-0.5 text-xs text-indigo-800 dark:bg-indigo-900 dark:text-indigo-200"
              >
                {{ scope }}
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
