<script setup lang="ts">
const auth = useAuth()
const router = useRouter()
const isSidebarCollapsed = ref(false)

const menuItems = [
  { icon: 'lucide:layout-dashboard', label: '仪表盘', to: '/' },
  { icon: 'lucide:users', label: '用户管理', to: '/users' },
  { icon: 'lucide:globe', label: '站点管理', to: '/sites' },
  { icon: 'lucide:key', label: 'OAuth 客户端', to: '/oauth-clients' },
  { icon: 'lucide:gamepad-2', label: '游戏管理', to: '/games' },
  { icon: 'lucide:shield', label: '内容审核', to: '/moderation' }
]

const handleLogout = async () => {
  await auth.logout()
}

onMounted(async () => {
  if (!auth.user.value) {
    await auth.fetchUser()
    if (!auth.user.value) {
      router.push('/auth/login')
    }
  }
})
</script>

<template>
  <div class="flex min-h-screen bg-gray-100 dark:bg-gray-900">
    <!-- Sidebar -->
    <aside
      :class="[
        'fixed inset-y-0 left-0 z-50 flex flex-col bg-white shadow-lg transition-all duration-300 dark:bg-gray-800',
        isSidebarCollapsed ? 'w-16' : 'w-64'
      ]"
    >
      <!-- Logo -->
      <div
        class="flex h-16 items-center justify-center border-b dark:border-gray-700"
      >
        <h1
          v-if="!isSidebarCollapsed"
          class="text-xl font-bold text-indigo-600"
        >
          KUN OAuth
        </h1>
        <Icon v-else name="lucide:key-round" class="size-8 text-indigo-600" />
      </div>

      <!-- Navigation -->
      <nav class="flex-1 space-y-1 p-4">
        <NuxtLink
          v-for="item in menuItems"
          :key="item.to"
          :to="item.to"
          class="flex items-center gap-3 rounded-lg px-3 py-2 text-gray-700 transition-colors hover:bg-indigo-50 hover:text-indigo-600 dark:text-gray-200 dark:hover:bg-gray-700"
          active-class="bg-indigo-50 text-indigo-600 dark:bg-gray-700"
        >
          <Icon :name="item.icon" class="size-5 shrink-0" />
          <span v-if="!isSidebarCollapsed">{{ item.label }}</span>
        </NuxtLink>
      </nav>

      <!-- Collapse Button -->
      <button
        class="flex h-12 items-center justify-center border-t text-gray-500 transition-colors hover:text-indigo-600 dark:border-gray-700"
        @click="isSidebarCollapsed = !isSidebarCollapsed"
      >
        <Icon
          :name="
            isSidebarCollapsed ? 'lucide:chevron-right' : 'lucide:chevron-left'
          "
          class="size-5"
        />
      </button>
    </aside>

    <!-- Main Content -->
    <div
      :class="[
        'flex flex-1 flex-col transition-all duration-300',
        isSidebarCollapsed ? 'ml-16' : 'ml-64'
      ]"
    >
      <!-- Top Bar -->
      <header
        class="sticky top-0 z-40 flex h-16 items-center justify-between border-b bg-white px-6 shadow-sm dark:border-gray-700 dark:bg-gray-800"
      >
        <div class="flex items-center gap-4">
          <h2 class="text-lg font-semibold text-gray-800 dark:text-white">
            管理后台
          </h2>
        </div>

        <div class="flex items-center gap-4">
          <!-- User Menu -->
          <div v-if="auth.user.value" class="flex items-center gap-3">
            <span class="text-sm text-gray-600 dark:text-gray-300">
              {{ auth.user.value.name }}
            </span>
            <KunAvatarAvatar
              :src="auth.user.value.avatar"
              :alt="auth.user.value.name"
              size="sm"
            />
            <button
              class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-red-600 dark:hover:bg-gray-700"
              title="退出登录"
              @click="handleLogout"
            >
              <Icon name="lucide:log-out" class="size-5" />
            </button>
          </div>
        </div>
      </header>

      <!-- Page Content -->
      <main class="flex-1 p-6">
        <slot />
      </main>
    </div>
  </div>
</template>
