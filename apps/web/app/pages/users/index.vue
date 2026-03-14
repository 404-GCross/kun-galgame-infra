<script setup lang="ts">
import type { User } from '~/composables/useAuth'

const api = useApi()

const users = ref<User[]>([])
const isLoading = ref(true)
const searchQuery = ref('')
const currentPage = ref(1)
const totalPages = ref(1)

const filteredUsers = computed(() => {
  if (!searchQuery.value) return users.value
  const query = searchQuery.value.toLowerCase()
  return users.value.filter(
    user =>
      user.name.toLowerCase().includes(query) ||
      user.email.toLowerCase().includes(query)
  )
})

const fetchUsers = async () => {
  isLoading.value = true
  try {
    // TODO: Add pagination API support
    // const response = await api.get<{ users: User[], total: number }>(`/users?page=${currentPage.value}`)
    // users.value = response.data.users
  } catch (error) {
    console.error('Failed to fetch users:', error)
  } finally {
    isLoading.value = false
  }
}

const getStatusBadge = (status: number) => {
  switch (status) {
    case 0:
      return { label: 'Active', class: 'bg-green-100 text-green-800' }
    case 1:
      return { label: 'Banned', class: 'bg-red-100 text-red-800' }
    default:
      return { label: 'Unknown', class: 'bg-gray-100 text-gray-800' }
  }
}

onMounted(() => {
  fetchUsers()
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-gray-800 dark:text-white">
          Users
        </h1>
        <p class="mt-1 text-gray-600 dark:text-gray-400">
          Manage user accounts across all sites
        </p>
      </div>
    </div>

    <!-- Search and Filters -->
    <div class="rounded-xl bg-white p-4 shadow-sm dark:bg-gray-800">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div class="relative flex-1">
          <Icon
            name="lucide:search"
            class="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-gray-400"
          />
          <input
            v-model="searchQuery"
            type="text"
            placeholder="Search users..."
            class="w-full rounded-lg border border-gray-200 py-2 pl-10 pr-4 focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-gray-700 dark:bg-gray-900"
          />
        </div>
      </div>
    </div>

    <!-- Users Table -->
    <div class="rounded-xl bg-white shadow-sm dark:bg-gray-800">
      <div v-if="isLoading" class="flex items-center justify-center py-12">
        <Icon name="lucide:loader-2" class="size-8 animate-spin text-indigo-500" />
      </div>

      <div v-else-if="filteredUsers.length === 0" class="py-12 text-center">
        <Icon name="lucide:users" class="mx-auto mb-4 size-12 text-gray-300" />
        <p class="text-gray-500 dark:text-gray-400">No users found</p>
        <p class="mt-1 text-sm text-gray-400">
          Users will appear here after migration
        </p>
      </div>

      <table v-else class="w-full">
        <thead class="border-b bg-gray-50 dark:border-gray-700 dark:bg-gray-900">
          <tr>
            <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              User
            </th>
            <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              Email
            </th>
            <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              Moemoepoint
            </th>
            <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              Status
            </th>
            <th class="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              Joined
            </th>
            <th class="px-6 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">
              Actions
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
          <tr v-for="user in filteredUsers" :key="user.uuid" class="hover:bg-gray-50 dark:hover:bg-gray-700">
            <td class="whitespace-nowrap px-6 py-4">
              <div class="flex items-center gap-3">
                <KunAvatarAvatar :src="user.avatar" :alt="user.name" size="sm" />
                <span class="font-medium text-gray-900 dark:text-white">
                  {{ user.name }}
                </span>
              </div>
            </td>
            <td class="whitespace-nowrap px-6 py-4 text-gray-500 dark:text-gray-400">
              {{ user.email }}
            </td>
            <td class="whitespace-nowrap px-6 py-4 text-gray-500 dark:text-gray-400">
              {{ user.moemoepoint }}
            </td>
            <td class="whitespace-nowrap px-6 py-4">
              <span
                :class="[
                  getStatusBadge(user.status).class,
                  'inline-flex rounded-full px-2 py-1 text-xs font-semibold'
                ]"
              >
                {{ getStatusBadge(user.status).label }}
              </span>
            </td>
            <td class="whitespace-nowrap px-6 py-4 text-gray-500 dark:text-gray-400">
              {{ new Date(user.created_at).toLocaleDateString() }}
            </td>
            <td class="whitespace-nowrap px-6 py-4 text-right">
              <button class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-600">
                <Icon name="lucide:more-horizontal" class="size-5" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
