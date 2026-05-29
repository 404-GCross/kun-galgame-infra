<script setup lang="ts">
const api = useApi()

const searchQuery = ref('') // bound to the input
const appliedSearch = ref('') // committed on submit (so we don't refetch per keystroke)
const currentPage = ref(1)
const limit = 20

// SSR-rendered (kungal-style). The reactive `query` makes useFetch refetch
// when the page or the applied search changes; first page is in the SSR HTML.
const { data, status, refresh } = await useApiFetch<{
  users: User[]
  total: number
  page: number
  total_pages: number
}>('/admin/users', {
  query: computed(() => ({
    page: currentPage.value,
    limit,
    ...(appliedSearch.value ? { search: appliedSearch.value } : {}),
  })),
})
const users = computed(() => data.value?.users ?? [])
const totalPages = computed(() => data.value?.total_pages ?? 1)
const totalUsers = computed(() => data.value?.total ?? 0)
const isLoading = computed(() => status.value === 'pending')

const handleSearch = () => {
  currentPage.value = 1
  appliedSearch.value = searchQuery.value
}

const handleBan = async (uuid: string) => {
  const response = await api.post(`/admin/users/${uuid}/ban`)
  if (response.code === 0) refresh()
}

const handleUnban = async (uuid: string) => {
  const response = await api.post(`/admin/users/${uuid}/unban`)
  if (response.code === 0) refresh()
}

const handleDeleteSessions = async (uuid: string) => {
  const response = await api.delete(`/admin/users/${uuid}/sessions`)
  if (response.code === 0) refresh()
}

const avatarUploadOpen = ref(false)
const avatarUploadTarget = ref<{ uuid: string; name: string } | null>(null)

const handleUploadAvatar = (user: { uuid: string; name: string }) => {
  avatarUploadTarget.value = user
  avatarUploadOpen.value = true
}

const onAvatarUploaded = () => {
  refresh()
}
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

    <KunCard content-class="justify-start gap-0" class-name="p-4">
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
    </KunCard>

    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-primary" />
    </div>

    <template v-else>
      <UsersTable
        :users="users"
        @ban="handleBan"
        @unban="handleUnban"
        @delete-sessions="handleDeleteSessions"
        @upload-avatar="handleUploadAvatar"
      />

      <div v-if="totalPages > 1" class="flex justify-center">
        <KunPagination
          v-model:current-page="currentPage"
          :total-page="totalPages"
          :is-loading="isLoading"
        />
      </div>
    </template>

    <UsersAvatarUploadModal
      v-model:open="avatarUploadOpen"
      :user="avatarUploadTarget"
      @success="onAvatarUploaded"
    />
  </div>
</template>
