<script setup lang="ts">
import { cn } from '@kungal/ui-core'
import { resolveAvatarUrl } from '~~/shared/utils/resolveImage'
import type { BagSession } from '~~/shared/types/user'
import { roleColor, roleLabel, primaryRole, needsStepUp } from '~/constants/roles'

// Avatar menu in the header with an in-place multi-account switcher.
// apps/web IS the OAuth provider, so switching is done via useAccountSwitch
// (no OAuth redirect). The session "bag" is loaded lazily — only the first
// time the menu opens — so it costs nothing on a normal page load.

const auth = useAuth()
const { listBagSessions, switchAccount, logoutAccount, logoutAllAccounts } =
  useAccountSwitch()

const cdnBase = useRuntimeConfig().public.imageCdnBase as string

// KunAvatar renders the URL as-is, so resolve the small (_100) variant for
// the tiny header tile. Falls back to the legacy `avatar` URL.
const headerAvatar = computed(() =>
  resolveAvatarUrl(auth.user.value, { cdnBase, variant: '100' }, '')
)

// Submenu state. The "切换账号" row toggles an inline expandable section
// inside the single popover panel — simpler and more robust than nesting a
// second KunPopover (no competing focus-trap / collision logic).
const isSwitcherOpen = ref(false)
const sessions = ref<BagSession[]>([])
const isLoading = ref(false)
const hasLoaded = ref(false)
// `sub` currently mid-switch — disables the rows + shows a spinner on it.
const switchingSub = ref<string | null>(null)

const popoverRef = useTemplateRef('popover')

const loadSessions = async () => {
  if (hasLoaded.value || isLoading.value) return
  isLoading.value = true
  try {
    sessions.value = await listBagSessions()
  } finally {
    isLoading.value = false
    hasLoaded.value = true
  }
}

// Lazy-load the bag when the menu opens; reset the submenu when it closes so
// it always reopens collapsed.
const onTriggerClick = () => {
  if (!hasLoaded.value) loadSessions()
}

const toggleSwitcher = () => {
  isSwitcherOpen.value = !isSwitcherOpen.value
  if (isSwitcherOpen.value) loadSessions()
}

const sessionAvatar = (session: BagSession) =>
  resolveAvatarUrl(session, { cdnBase, variant: '100' }, '')

const goStepUp = (session: BagSession) =>
  // Privileged target (admin/ren) — must re-authenticate, no silent switch.
  // force=1 keeps the login form visible (we're still logged in as the current
  // account); pre-fill the target's email so it's password-only.
  navigateTo(`/auth/login?force=1&account=${encodeURIComponent(session.email)}`)

const handleSwitch = async (session: BagSession) => {
  if (session.active || switchingSub.value) return
  // Admin/ren always need fresh re-auth — go straight to step-up (skip the
  // switch call, which would just return 10016). Role is known from the bag.
  if (needsStepUp(session.roles)) {
    await goStepUp(session)
    return
  }
  switchingSub.value = session.sub
  try {
    const result = await switchAccount(session.sub)
    if (result.ok) {
      // Hard reload so every page/store reflects the new active account.
      window.location.reload()
      return
    }
    if (result.stepUp) {
      await goStepUp(session)
      return
    }
    // Switch failed (account no longer in the bag, etc.) — refresh the list
    // so the UI reflects reality instead of leaving a dead row.
    hasLoaded.value = false
    await loadSessions()
  } finally {
    switchingSub.value = null
  }
}

const handleAddAccount = async () => {
  popoverRef.value?.close()
  // force=1 so the login form shows even though we're logged in (the point is
  // to authenticate a different account) — see LoginForm.
  await navigateTo('/auth/login?force=1')
}

// Log out ONLY the current account. If a non-privileged account remains in the
// bag we land on it (you stay signed in to your other accounts, Gmail-mobile
// style); otherwise it's a full logout. We switch FIRST so the caller stays a
// bag member (passes the confused-deputy guard), then remove the old account.
const handleLogoutCurrent = async () => {
  popoverRef.value?.close()
  if (!hasLoaded.value) await loadSessions()
  const currentSub = auth.user.value?.uuid
  const next = sessions.value.find(
    (s) => s.sub !== currentSub && !needsStepUp(s.roles)
  )
  if (next && currentSub) {
    const result = await switchAccount(next.sub)
    if (result.ok) {
      await logoutAccount(currentSub)
      window.location.reload()
      return
    }
  }
  // Last account, switch failed, or only privileged accounts remain → full logout.
  await auth.logout()
}

// Log out EVERY account in this browser's bag.
const handleLogoutAll = async () => {
  popoverRef.value?.close()
  await logoutAllAccounts()
  // auth.logout() POSTs /auth/logout (now a no-op — the session is already gone)
  // but reuses its clearAuth + redirect-to-login.
  await auth.logout()
}
</script>

<template>
  <KunPopover
    v-if="auth.user.value"
    ref="popover"
    position="bottom-end"
    inner-class="w-72"
  >
    <template #trigger>
      <button
        type="button"
        class="flex items-center gap-2 rounded-full transition-opacity hover:opacity-80 md:gap-3"
        aria-label="账号菜单"
        @click="onTriggerClick"
      >
        <span class="text-default-500 hidden text-sm sm:inline">
          {{ auth.user.value.name }}
        </span>
        <KunAvatar
          :user="{ id: 0, name: auth.user.value.name, avatar: headerAvatar }"
          size="md"
          :is-navigation="false"
        />
      </button>
    </template>

    <div class="py-1">
      <!-- Active account header -->
      <div class="border-default-200 flex items-center gap-3 border-b px-3 pb-3 pt-2">
        <KunAvatar
          :user="{ id: 0, name: auth.user.value.name, avatar: headerAvatar }"
          size="md"
          :is-navigation="false"
        />
        <div class="min-w-0 flex-1">
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

      <!-- 切换账号 — expandable inline submenu -->
      <button
        type="button"
        class="text-default-500 hover:bg-default-100 hover:text-foreground flex w-full items-center gap-3 px-3 py-2 text-sm transition-colors"
        @click="toggleSwitcher"
      >
        <KunIcon name="lucide:users" class="size-4 shrink-0" />
        <span class="flex-1 text-left">切换账号</span>
        <KunIcon
          :name="isSwitcherOpen ? 'lucide:chevron-up' : 'lucide:chevron-down'"
          class="size-4 shrink-0"
        />
      </button>

      <div v-if="isSwitcherOpen" class="bg-default-50 py-1">
        <div
          v-if="isLoading"
          class="text-default-400 flex items-center justify-center gap-2 px-3 py-3 text-sm"
        >
          <KunIcon name="lucide:loader-circle" class="size-4 animate-spin" />
          <span>加载中...</span>
        </div>

        <template v-else>
          <button
            v-for="session in sessions"
            :key="session.sub"
            type="button"
            :disabled="!!switchingSub"
            :class="
              cn(
                'flex w-full items-center gap-3 px-3 py-2 text-left transition-colors',
                session.active
                  ? 'cursor-default'
                  : 'hover:bg-default-100 disabled:cursor-not-allowed disabled:opacity-60'
              )
            "
            @click="handleSwitch(session)"
          >
            <KunAvatar
              :user="{ id: 0, name: session.name, avatar: sessionAvatar(session) }"
              size="sm"
              :is-navigation="false"
            />
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-1.5">
                <p class="text-foreground truncate text-sm">{{ session.name }}</p>
                <KunChip
                  v-if="primaryRole(session.roles)"
                  :color="roleColor(primaryRole(session.roles))"
                  variant="flat"
                  size="sm"
                >
                  {{ roleLabel(primaryRole(session.roles)) }}
                </KunChip>
              </div>
              <p class="text-default-400 truncate text-xs">{{ session.email }}</p>
              <p
                v-if="!session.active && needsStepUp(session.roles)"
                class="text-warning text-xs"
              >
                切换管理员账号需要重新登录
              </p>
            </div>
            <KunIcon
              v-if="switchingSub === session.sub"
              name="lucide:loader-circle"
              class="text-default-400 size-4 shrink-0 animate-spin"
            />
            <span
              v-else-if="session.active"
              class="text-primary shrink-0 text-xs font-medium"
            >
              当前
            </span>
          </button>

          <p
            v-if="!sessions.length"
            class="text-default-400 px-3 py-2 text-xs"
          >
            暂无其他账号
          </p>
        </template>

        <!-- 添加账号 -->
        <button
          type="button"
          class="text-primary hover:bg-default-100 flex w-full items-center gap-3 px-3 py-2 text-sm transition-colors"
          @click="handleAddAccount"
        >
          <KunIcon name="lucide:plus" class="size-4 shrink-0" />
          <span>添加账号</span>
        </button>
      </div>

      <!-- 退出：当前账号 vs 全部账号 -->
      <button
        type="button"
        class="text-danger hover:bg-danger-50 border-default-200 mt-1 flex w-full items-center gap-3 border-t px-3 py-2 text-sm transition-colors"
        @click="handleLogoutCurrent"
      >
        <KunIcon name="lucide:log-out" class="size-4 shrink-0" />
        <span>退出当前账号</span>
      </button>
      <button
        v-if="sessions.length > 1"
        type="button"
        class="text-default-500 hover:bg-default-100 hover:text-danger flex w-full items-center gap-3 px-3 py-2 text-sm transition-colors"
        @click="handleLogoutAll"
      >
        <KunIcon name="lucide:log-out" class="size-4 shrink-0" />
        <span>退出全部账号</span>
      </button>
    </div>
  </KunPopover>
</template>
