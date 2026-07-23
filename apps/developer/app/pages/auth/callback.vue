<script setup lang="ts">
// OAuth redirect landing. Validates state (CSRF), posts { code, code_verifier }
// to the server exchange route (which lands the session cookies), seeds the
// access_token ref + user store, then continues to the stored redirect (or the
// console). On any failure it shows a retry that reopens the login modal.
import { isSafeInternalPath } from '~/utils/safe-path'

definePageMeta({ layout: false })

useSeoMeta({ title: '登录中', robots: 'noindex' })

const route = useRoute()
const auth = useAuth()
const { open: openLogin } = useLoginModal()

const error = ref('')

onMounted(async () => {
  const code = route.query.code as string | undefined
  const state = route.query.state as string | undefined
  const oauthError = route.query.error as string | undefined

  const savedState = sessionStorage.getItem('oauth_state')
  const verifier = sessionStorage.getItem('oauth_code_verifier')
  const redirect = sessionStorage.getItem('oauth_redirect')
  sessionStorage.removeItem('oauth_state')
  sessionStorage.removeItem('oauth_code_verifier')
  sessionStorage.removeItem('oauth_redirect')

  if (oauthError) {
    error.value = '授权被取消或失败，请重试'
    return
  }
  if (!code || !state || state !== savedState || !verifier) {
    error.value = '登录校验失败，请重新登录'
    return
  }

  try {
    const res = await $fetch<{
      code: number
      message?: string
      data?: { access_token: string }
    }>('/auth/exchange', {
      method: 'POST',
      body: { code, code_verifier: verifier }
    })
    if (res.code !== 0 || !res.data?.access_token) {
      error.value = res.message || '登录失败，请重试'
      return
    }
    // Seed the client cookie ref (the server Set-Cookie won't update it), then
    // hydrate the user before leaving this page.
    auth.setAccessToken(res.data.access_token)
    await auth.fetchUser()
  } catch {
    error.value = '登录失败，请重试'
    return
  }

  const dest = isSafeInternalPath(redirect) ? redirect : '/dashboard'
  await navigateTo(dest)
})

const retry = async () => {
  await navigateTo('/')
  openLogin()
}
</script>

<template>
  <div
    class="flex min-h-screen flex-col items-center justify-center gap-4 bg-background px-4 text-center"
  >
    <template v-if="error">
      <KunIcon name="lucide:circle-alert" class="size-10 text-danger" />
      <p class="text-foreground">{{ error }}</p>
      <KunButton color="primary" @click="retry">返回重新登录</KunButton>
    </template>
    <template v-else>
      <KunIcon
        name="lucide:loader-circle"
        class="size-8 animate-spin text-primary"
      />
      <p class="text-default-500">正在登录…</p>
    </template>
  </div>
</template>
