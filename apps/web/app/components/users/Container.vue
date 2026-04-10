<script setup lang="ts">
const api = useApi()

const users = ref<User[]>([])
const isLoading = ref(true)
const searchQuery = ref('')
const currentPage = ref(1)
const totalPages = ref(1)
const totalUsers = ref(0)
const limit = 20

const fetchUsers = async () => {
  isLoading.value = true
  try {
    const params = new URLSearchParams({
      page: String(currentPage.value),
      limit: String(limit),
    })
    if (searchQuery.value) {
      params.set('search', searchQuery.value)
    }

    const response = await api.get<{
      users: User[]
      total: number
      page: number
      total_pages: number
    }>(`/admin/users?${params}`)

    if (response.code === 0) {
      users.value = response.data.users || []
      totalPages.value = response.data.total_pages
      totalUsers.value = response.data.total
    }
  } finally {
    isLoading.value = false
  }
}

const handleSearch = () => {
  currentPage.value = 1
  fetchUsers()
}

const handleBan = async (uuid: string) => {
  const response = await api.post(`/admin/users/${uuid}/ban`)
  if (response.code === 0) fetchUsers()
}

const handleUnban = async (uuid: string) => {
  const response = await api.post(`/admin/users/${uuid}/unban`)
  if (response.code === 0) fetchUsers()
}

const handleDeleteSessions = async (uuid: string) => {
  const response = await api.delete(`/admin/users/${uuid}/sessions`)
  if (response.code === 0) fetchUsers()
}

watch(currentPage, () => fetchUsers())

onMounted(() => fetchUsers())
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-foreground">用户管理</h1>
        <p class="mt-1 text-default-500">
          共 {{ totalUsers }} 个用户
        </p>
      </div>
    </div>

    <div class="rounded-xl bg-content1 p-4 shadow-sm">
      <form class="flex gap-3" @submit.prevent="handleSearch">
        <KunInput
          v-model="searchQuery"
          type="text"
          placeholder="搜索用户名或邮箱..."
          class="flex-1"
        />
        <KunButton color="primary" type="submit" :disabled="isLoading">
          <Icon name="lucide:search" class="mr-1 size-4" />
          搜索
        </KunButton>
      </form>
    </div>

    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-primary" />
    </div>

    <template v-else>
      <UsersTable
        :users="users"
        @ban="handleBan"
        @unban="handleUnban"
        @delete-sessions="handleDeleteSessions"

      />

      <div v-if="totalPages > 1" class="flex justify-center">
        <KunPagination
          v-model:current-page="currentPage"
          :total-page="totalPages"
          :is-loading="isLoading"
        />
      </div>
    </template>
  </div>
</template>
