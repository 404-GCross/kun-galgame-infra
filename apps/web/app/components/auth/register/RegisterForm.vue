<script setup lang="ts">
const auth = useAuth()
const router = useRouter()
const route = useRoute()

const name = ref('')
const email = ref('')
const password = ref('')
const confirmPassword = ref('')
const error = ref('')
const isLoading = ref(false)

// `?redirect=` lands here when a downstream site (kungal / moyu / wiki)
// bounced the user to /auth/register as part of the unified registration
// flow. After register+auto-login the value is typically an /oauth/authorize
// URL — going there immediately keeps the OAuth code round-trip seamless
// (consent UI is silently skipped for first-party clients, code is issued,
// user lands back on the originating site already authenticated).
//
// Same redirect-after-auth contract LoginForm uses; kept identical so a
// downstream can build one URL helper that targets either /auth/login or
// /auth/register interchangeably depending on which button the user clicked.
//
// Mirrors LoginForm's navigateAfterLogin shape exactly so the two pages
// behave identically — important for unified PKCE handlers that don't know
// which endpoint they'll land the user on.
const redirectUrl = computed(() => route.query.redirect as string | undefined)

const navigateAfterRegister = () => {
  if (redirectUrl.value) {
    if (redirectUrl.value.startsWith('/')) {
      router.push(redirectUrl.value)
    } else {
      window.location.href = redirectUrl.value
    }
  } else {
    router.push('/profile')
  }
}

// Already-logged-in user landing on /auth/register: don't confuse them
// with a blank form. If they came in via the unified flow (?redirect=...)
// just continue the OAuth handoff; otherwise drop them on /profile. This
// matches LoginForm's onMounted behaviour.
onMounted(() => {
  if (auth.isLoggedIn.value) {
    navigateAfterRegister()
  }
})

const handleSubmit = async () => {
  error.value = ''

  if (password.value !== confirmPassword.value) {
    error.value = '两次输入的密码不一致'
    return
  }
  if (password.value.length < 6) {
    error.value = '密码长度至少为 6 位'
    return
  }

  isLoading.value = true
  try {
    const response = await auth.register(name.value, email.value, password.value)
    if (response.code === 0) {
      navigateAfterRegister()
    } else {
      error.value = response.message || '注册失败'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '注册失败'
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunCard class="p-8">
    <div class="mb-8 text-center">
      <h1 class="text-2xl font-bold text-foreground">创建账号</h1>
      <p class="mt-2 text-default-500">加入 KUN Visual Novel 社区</p>
    </div>

    <form @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <KunInput v-model="name" label="用户名" type="text" placeholder="请输入用户名" required autofocus />
        <KunInput v-model="email" label="邮箱" type="email" placeholder="请输入邮箱" required />
        <KunInput v-model="password" label="密码" type="password" placeholder="请输入密码" required />
        <KunInput v-model="confirmPassword" label="确认密码" type="password" placeholder="请再次输入密码" required />

        <div v-if="error" class="rounded-lg bg-danger-50 p-3 text-sm text-danger">{{ error }}</div>

        <KunButton type="submit" color="primary" class="w-full" :disabled="isLoading">
          <Icon v-if="isLoading" name="lucide:loader-2" class="mr-2 size-4 animate-spin" />
          {{ isLoading ? '注册中...' : '注册' }}
        </KunButton>
      </div>
    </form>

    <div class="mt-6 text-center text-sm">
      <p class="text-default-500">
        已有账号？
        <NuxtLink
          :to="redirectUrl ? `/auth/login?redirect=${encodeURIComponent(redirectUrl)}` : '/auth/login'"
          class="text-primary hover:underline"
        >立即登录</NuxtLink>
      </p>
    </div>
  </KunCard>
</template>
