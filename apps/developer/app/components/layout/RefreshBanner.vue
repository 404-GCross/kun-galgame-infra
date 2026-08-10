<script setup lang="ts">
import {
  REFRESH_TRANSIENT,
  requestTokenRefresh,
  useRefreshTransient
} from '~/composables/useTokenRefresh'

// signal was silently empty pages. This banner names the state and offers a
const transient = useRefreshTransient()
const auth = useAuth()
const userStore = useUserStore()
const accessToken = useCookie('access_token')

const authMode = useCookie('auth_mode')
const visible = computed(() => transient.value && Boolean(authMode.value))

const retrying = ref(false)

const retry = async () => {
  if (retrying.value) return
  retrying.value = true
  try {
    const result = await requestTokenRefresh()
    if (typeof result === 'string') {
      auth.setAccessToken(result)
      await auth.fetchUser()
      await refreshNuxtData()
      return
    }
    if (result === REFRESH_TRANSIENT) {
      return // still down — the flag stays up, the user can retry again
    }
    accessToken.value = null
    authMode.value = null
    userStore.clearUser()
    transient.value = false
    const here = window.location.pathname + window.location.search
    navigateTo(`/login?redirect=${encodeURIComponent(here)}`)
  } finally {
    retrying.value = false
  }
}

const dismiss = () => {
  transient.value = false // reappears on the next transient failure
}
</script>

<template>
  <div
    v-if="visible"
    role="alert"
    class="fixed inset-x-4 bottom-4 z-50 mx-auto max-w-xl rounded-xl border border-warning-200 bg-warning-50 px-4 py-3 shadow-lg"
  >
    <div class="flex flex-wrap items-center gap-3">
      <KunIcon
        name="lucide:triangle-alert"
        class="size-5 shrink-0 text-warning"
      />
      <p class="min-w-0 flex-1 text-sm text-warning-700">
        会话刷新暂时失败（网络或服务波动），你的登录状态仍然有效。
      </p>
      <div class="flex shrink-0 items-center gap-2">
        <KunButton
          color="warning"
          size="sm"
          :disabled="retrying"
          @click="retry"
        >
          <KunIcon
            v-if="retrying"
            name="lucide:loader-circle"
            class="mr-1.5 size-4 animate-spin"
          />
          <KunIcon v-else name="lucide:refresh-cw" class="mr-1.5 size-4" />
          {{ retrying ? '重试中…' : '重试' }}
        </KunButton>
        <button
          type="button"
          class="rounded-lg px-2 py-1 text-sm text-warning-700 transition-colors hover:bg-warning-100"
          @click="dismiss"
        >
          忽略
        </button>
      </div>
    </div>
  </div>
</template>
