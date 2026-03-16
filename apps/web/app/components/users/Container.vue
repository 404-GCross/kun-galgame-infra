<script setup lang="ts">
const api = useApi()

const users = ref<User[]>([])
const isLoading = ref(true)
const searchQuery = ref('')

const filteredUsers = computed(() => {
  if (!searchQuery.value) return users.value
  const query = searchQuery.value.toLowerCase()
  return users.value.filter(
    user => user.name.toLowerCase().includes(query) || user.email.toLowerCase().includes(query)
  )
})

const fetchUsers = async () => {
  isLoading.value = true
  try {
    // TODO: call /admin/users with pagination
  } catch (error) {
    console.error('Failed to fetch users:', error)
  } finally {
    isLoading.value = false
  }
}

onMounted(() => fetchUsers())
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-foreground">用户管理</h1>
        <p class="mt-1 text-default-500">管理所有站点的用户账号</p>
      </div>
    </div>

    <div class="rounded-xl bg-content1 p-4 shadow-sm">
      <KunInput
        v-model="searchQuery"
        type="text"
        placeholder="搜索用户..."
      />
    </div>

    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-primary" />
    </div>

    <UsersTable v-else :users="filteredUsers" />
  </div>
</template>
