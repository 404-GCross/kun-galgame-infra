<script setup lang="ts">
const auth = useAuth()
const router = useRouter()
const route = useRoute()

useSeoMeta({
  title: '登录',
  description: '登录 NextMoe 生态账号，管理你的开放 API 应用与密钥。'
})

const account = ref('')
const password = ref('')
const error = ref('')
const isLoading = ref(false)

const redirectUrl = computed(() => route.query.redirect as string | undefined)

// Only follow a redirect that stays on THIS origin (blocks open-redirect).
const isSafeRedirect = (url: string): boolean => {
  if (url.startsWith('//')) return false
  return url.startsWith('/')
}

const navigateAfterLogin = () => {
  const r = redirectUrl.value
  router.push(r && isSafeRedirect(r) ? r : '/dashboard')
}

const handleSubmit = async () => {
  error.value = ''
  isLoading.value = true
  try {
    const response = await auth.login(account.value, password.value)
    if (response.code !== 0) {
      error.value = response.message || '登录失败'
      return
    }
    navigateAfterLogin()
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '登录失败'
  } finally {
    isLoading.value = false
  }
}

// Already logged in → skip straight to the destination.
onMounted(() => {
  if (auth.isLoggedIn.value) {
    navigateAfterLogin()
  }
})
</script>

<template>
  <div class="mx-auto flex max-w-md flex-col justify-center py-8">
    <KunCard class="p-8">
      <div class="mb-8 text-center">
        <h1 class="text-2xl font-bold text-foreground">欢迎回来</h1>
        <p class="mt-2 text-default-500">使用 NextMoe 生态账号登录开发者平台</p>
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

          <div
            v-if="error"
            class="rounded-lg bg-danger-50 p-3 text-sm text-danger"
          >
            {{ error }}
          </div>

          <KunButton
            type="submit"
            color="primary"
            class="w-full"
            :disabled="isLoading"
          >
            <KunIcon
              v-if="isLoading"
              name="lucide:loader-circle"
              class="mr-2 size-4 animate-spin"
            />
            {{ isLoading ? '登录中...' : '登录' }}
          </KunButton>
        </div>
      </form>

      <p class="mt-6 text-center text-sm text-default-500">
        还没有生态账号？请前往
        <a
          href="https://www.kungal.com"
          target="_blank"
          rel="noopener noreferrer"
          class="text-primary hover:underline"
        >
          鲲 Galgame
        </a>
        注册。
      </p>
    </KunCard>
  </div>
</template>
