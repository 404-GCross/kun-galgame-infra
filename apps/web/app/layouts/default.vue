<script setup lang="ts">
import { SIDEBAR_MENU } from '~/constants/admin'

const auth = useAuth()
const router = useRouter()
const isSidebarCollapsed = ref(false)

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
  <div class="bg-background flex min-h-screen">
    <!-- Sidebar -->
    <aside
      :class="[
        'bg-content1 fixed inset-y-0 left-0 z-50 flex flex-col shadow-lg transition-all duration-300',
        isSidebarCollapsed ? 'w-16' : 'w-64'
      ]"
    >
      <!-- Logo -->
      <div
        class="border-default-200 flex h-16 items-center justify-center border-b"
      >
        <h1 v-if="!isSidebarCollapsed" class="text-primary text-xl font-bold">
          KUN OAuth
        </h1>
        <Icon v-else name="lucide:key-round" class="text-primary size-8" />
      </div>

      <!-- Navigation -->
      <nav class="flex-1 space-y-1 p-4">
        <NuxtLink
          v-for="item in SIDEBAR_MENU"
          :key="item.to"
          :to="item.to"
          class="text-default-600 hover:bg-primary-50 hover:text-primary flex items-center gap-3 rounded-lg px-3 py-2 transition-colors"
          active-class="bg-primary-50 text-primary"
        >
          <Icon :name="item.icon" class="size-5 shrink-0" />
          <span v-if="!isSidebarCollapsed">{{ item.label }}</span>
        </NuxtLink>
      </nav>

      <!-- Collapse Button -->
      <button
        class="border-default-200 text-default-400 hover:text-primary flex h-12 items-center justify-center border-t transition-colors"
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
        class="border-default-200 bg-content1 sticky top-0 z-40 flex h-16 items-center justify-between border-b px-6 shadow-sm"
      >
        <div class="flex items-center gap-4">
          <h2 class="text-foreground text-lg font-semibold">管理后台</h2>
        </div>

        <div class="flex items-center gap-4">
          <!-- User Menu -->
          <div v-if="auth.user.value" class="flex items-center gap-3">
            <span class="text-default-500 text-sm">
              {{ auth.user.value.name }}
            </span>
            <KunAvatar
              :src="auth.user.value.avatar"
              :alt="auth.user.value.name"
              size="sm"
            />
            <button
              class="text-default-400 hover:bg-default-100 hover:text-danger rounded-lg p-2 transition-colors"
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
