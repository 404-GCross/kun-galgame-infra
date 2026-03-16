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
  <div class="flex min-h-screen bg-background">
    <!-- Sidebar -->
    <aside
      :class="[
        'fixed inset-y-0 left-0 z-50 flex flex-col bg-content1 shadow-lg transition-all duration-300',
        isSidebarCollapsed ? 'w-16' : 'w-64'
      ]"
    >
      <!-- Logo -->
      <div
        class="flex h-16 items-center justify-center border-b border-default-200"
      >
        <h1
          v-if="!isSidebarCollapsed"
          class="text-xl font-bold text-primary"
        >
          KUN OAuth
        </h1>
        <Icon v-else name="lucide:key-round" class="size-8 text-primary" />
      </div>

      <!-- Navigation -->
      <nav class="flex-1 space-y-1 p-4">
        <NuxtLink
          v-for="item in SIDEBAR_MENU"
          :key="item.to"
          :to="item.to"
          class="flex items-center gap-3 rounded-lg px-3 py-2 text-default-600 transition-colors hover:bg-primary-50 hover:text-primary"
          active-class="bg-primary-50 text-primary"
        >
          <Icon :name="item.icon" class="size-5 shrink-0" />
          <span v-if="!isSidebarCollapsed">{{ item.label }}</span>
        </NuxtLink>
      </nav>

      <!-- Collapse Button -->
      <button
        class="flex h-12 items-center justify-center border-t border-default-200 text-default-400 transition-colors hover:text-primary"
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
        class="sticky top-0 z-40 flex h-16 items-center justify-between border-b border-default-200 bg-content1 px-6 shadow-sm"
      >
        <div class="flex items-center gap-4">
          <h2 class="text-lg font-semibold text-foreground">
            管理后台
          </h2>
        </div>

        <div class="flex items-center gap-4">
          <!-- User Menu -->
          <div v-if="auth.user.value" class="flex items-center gap-3">
            <span class="text-sm text-default-500">
              {{ auth.user.value.name }}
            </span>
            <KunAvatarAvatar
              :src="auth.user.value.avatar"
              :alt="auth.user.value.name"
              size="sm"
            />
            <button
              class="rounded-lg p-2 text-default-400 transition-colors hover:bg-default-100 hover:text-danger"
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
