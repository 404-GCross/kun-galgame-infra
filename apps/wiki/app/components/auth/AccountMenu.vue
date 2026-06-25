<script setup lang="ts">
// Avatar menu in the header with a multi-account switcher.
//
// See docs/integration/oauth/09-account-switching.md §3.6. Wiki is a
// cross-origin downstream OAuth client, so it cannot switch accounts in place:
// every switch / add re-runs the authorize redirect to the OP, which already
// holds the browser's session bag and renders the chooser. The submenu's
// account list comes from a LOCAL best-effort cache (useKnownAccounts) — it
// only shows accounts this browser has seen on wiki, and the OP bag stays the
// source of truth (a stale entry gracefully falls back to login).
import { resolveAvatarUrl } from '~/shared/utils/resolveImage'
import type { KnownAccount } from '~/composables/useKnownAccounts'
import { roleColor, roleLabel, primaryRole, needsStepUp } from '~/constants/roles'

const auth = useAuth()
const { login } = useOAuthLogin()
const { accounts } = useKnownAccounts()
const cdnBase = useRuntimeConfig().public.imageCdnBase as string

// Small (_100) variant for the tiny header avatar; prefers the image_service
// hash, falls back to the legacy `avatar` URL.
const headerAvatar = computed(() =>
  resolveAvatarUrl(auth.user.value, { cdnBase, variant: '100' }, '')
)

const accountAvatar = (account: KnownAccount) =>
  resolveAvatarUrl(account, { cdnBase, variant: '100' }, '')

const currentSub = computed(() => auth.user.value?.uuid ?? '')

// The submenu lists every cached account EXCEPT the one that is currently
// active (no point "switching" to who you already are).
const switchable = computed(() =>
  accounts.value.filter((a) => a.sub !== currentSub.value)
)

// "切换账号" expands inline (accordion-style) rather than as a side popover:
// KunPopover only opens top/bottom, and an inline expand works identically on
// desktop hover and mobile tap.
const isSubmenuOpen = ref(false)
const toggleSubmenu = () => {
  isSubmenuOpen.value = !isSubmenuOpen.value
}

// Switch to a known account: top-level redirect with prompt=select_account +
// login_hint so the OP silently activates that account (no chooser UI when
// unambiguous). PKCE + one-time state are handled inside login().
const switchTo = (account: KnownAccount) => {
  login({ prompt: 'select_account', loginHint: account.sub })
}

// Add another account: open the chooser with no hint so the user can pick
// "use another account" / sign in fresh.
const addAccount = () => {
  login({ prompt: 'select_account' })
}

// Reuse the existing logout-scope chooser.
const showLogoutModal = ref(false)
const handleLogout = () => {
  showLogoutModal.value = true
}
</script>

<template>
  <div v-if="auth.user.value" class="flex items-center gap-2 md:gap-3">
    <span class="text-default-500 hidden text-sm sm:inline">
      {{ auth.user.value.name }}
    </span>

    <KunPopover position="bottom-end">
      <template #trigger>
        <button type="button" aria-label="账号菜单" class="rounded-full">
          <KunAvatar
            :user="{ id: 0, name: auth.user.value.name, avatar: headerAvatar }"
            size="md"
            :is-navigation="false"
          />
        </button>
      </template>

      <div class="w-64 py-1">
      <!-- Current account header -->
      <div class="border-default-200 flex items-center gap-3 border-b px-3 py-3">
        <KunAvatar
          :user="{ id: 0, name: auth.user.value.name, avatar: headerAvatar }"
          size="sm"
          :is-navigation="false"
        />
        <div class="min-w-0">
          <div class="flex items-center gap-1.5">
            <p class="text-foreground truncate text-sm font-medium">
              {{ auth.user.value.name }}
            </p>
            <KunChip
              v-if="primaryRole(auth.user.value.roles)"
              :color="roleColor(primaryRole(auth.user.value.roles))"
              variant="flat"
              size="sm"
            >
              {{ roleLabel(primaryRole(auth.user.value.roles)) }}
            </KunChip>
          </div>
          <p class="text-default-400 truncate text-xs">
            {{ auth.user.value.email }}
          </p>
        </div>
      </div>

      <!-- 切换账号 — expands inline into the cached account list -->
      <button
        type="button"
        class="text-default-500 hover:bg-default-100 hover:text-foreground flex w-full items-center justify-between gap-3 px-3 py-2 text-sm transition-colors"
        @click="toggleSubmenu"
      >
        <span class="flex items-center gap-3">
          <KunIcon name="lucide:users" class="size-4" />
          <span>切换账号</span>
        </span>
        <KunIcon
          :name="isSubmenuOpen ? 'lucide:chevron-up' : 'lucide:chevron-down'"
          class="size-4"
        />
      </button>

      <div v-if="isSubmenuOpen" class="bg-default-50 border-default-200 border-y">
        <button
          v-for="account in switchable"
          :key="account.sub"
          type="button"
          class="hover:bg-default-100 flex w-full items-center gap-3 px-3 py-2 text-left transition-colors"
          @click="switchTo(account)"
        >
          <KunAvatar
            :user="{ id: 0, name: account.name, avatar: accountAvatar(account) }"
            size="sm"
            :is-navigation="false"
          />
          <div class="min-w-0">
            <div class="flex items-center gap-1.5">
              <p class="text-foreground truncate text-sm">{{ account.name }}</p>
              <KunChip
                v-if="primaryRole(account.roles)"
                :color="roleColor(primaryRole(account.roles))"
                variant="flat"
                size="sm"
              >
                {{ roleLabel(primaryRole(account.roles)) }}
              </KunChip>
            </div>
            <p class="text-default-400 truncate text-xs">{{ account.email }}</p>
            <p v-if="needsStepUp(account.roles)" class="text-warning text-xs">
              切换管理员账号需要重新登录
            </p>
          </div>
        </button>

        <p
          v-if="switchable.length === 0"
          class="text-default-400 px-3 py-2 text-xs"
        >
          本浏览器暂无其它已知账号
        </p>

        <!-- 添加账号 — chooser with no hint -->
        <button
          type="button"
          class="text-primary hover:bg-default-100 flex w-full items-center gap-3 px-3 py-2 text-sm transition-colors"
          @click="addAccount"
        >
          <KunIcon name="lucide:user-plus" class="size-4" />
          <span>添加账号</span>
        </button>
      </div>

      <!-- 退出登录 -->
      <button
        type="button"
        class="text-danger hover:bg-danger-50 flex w-full items-center gap-3 px-3 py-2 text-sm transition-colors"
        @click="handleLogout"
      >
        <KunIcon name="lucide:log-out" class="size-4" />
        <span>退出登录</span>
      </button>
      </div>
    </KunPopover>

    <AuthLogoutModal v-model="showLogoutModal" />
  </div>
</template>
