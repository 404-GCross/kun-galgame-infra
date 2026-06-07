<script setup lang="ts">
const auth = useAuth()
const router = useRouter()
const route = useRoute()

const account = ref('')
const password = ref('')
const error = ref('')
const isLoading = ref(false)

// If redirected from OAuth authorize, go back after login
const redirectUrl = computed(() => route.query.redirect as string | undefined)

const navigateAfterLogin = () => {
  if (redirectUrl.value) {
    // Check if redirect is a relative path (same-domain) or absolute URL
    if (redirectUrl.value.startsWith('/')) {
      router.push(redirectUrl.value)
    } else {
      window.location.href = redirectUrl.value
    }
  } else {
    router.push(auth.isAdmin.value ? '/' : '/profile')
  }
}

const handleSubmit = async () => {
  error.value = ''
  isLoading.value = true

  try {
    const response = await auth.login(account.value, password.value)
    if (response.code === 0) {
      navigateAfterLogin()
    } else {
      error.value = response.message || '登录失败'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '登录失败'
  } finally {
    isLoading.value = false
  }
}

onMounted(async () => {
  if (auth.isLoggedIn.value) {
    navigateAfterLogin()
  }
})
</script>

<template>
  <KunCard class="p-8">
    <div class="mb-8 text-center">
      <h1 class="text-2xl font-bold text-foreground">欢迎回来</h1>
      <p class="mt-2 text-default-500">登录 鲲 Galgame OAuth 管理后台</p>
    </div>

    <form @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <KunInput
          v-model="account"
          label="账号"
          type="text"
          placeholder="请输入邮箱或用户名"
          required
          autofocus
        />

        <KunInput
          v-model="password"
          label="密码"
          type="password"
          placeholder="请输入密码"
          required
        />

        <div v-if="error" class="rounded-lg bg-danger-50 p-3 text-sm text-danger">
          {{ error }}
        </div>

        <KunButton type="submit" color="primary" class="w-full" :disabled="isLoading">
          <KunIcon v-if="isLoading" name="lucide:loader-circle" class="mr-2 size-4 animate-spin" />
          {{ isLoading ? '登录中...' : '登录' }}
        </KunButton>
      </div>
    </form>

    <div class="mt-6 space-y-4 text-center text-sm">
      <NuxtLink to="/auth/forgot-password" class="text-primary hover:underline">
        忘记密码？
      </NuxtLink>
      <p class="text-default-500">
        还没有账号？
        <NuxtLink
          :to="redirectUrl ? `/auth/register?redirect=${encodeURIComponent(redirectUrl)}` : '/auth/register'"
          class="text-primary hover:underline"
        >立即注册</NuxtLink>
      </p>
    </div>
  </KunCard>
</template>
