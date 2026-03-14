<script setup lang="ts">
definePageMeta({
  layout: 'auth'
})

const auth = useAuth()
const router = useRouter()

const email = ref('')
const password = ref('')
const error = ref('')
const isLoading = ref(false)

const handleSubmit = async () => {
  error.value = ''
  isLoading.value = true

  try {
    const response = await auth.login(email.value, password.value)
    if (response.code === 0) {
      router.push('/')
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
    router.push('/')
  }
})
</script>

<template>
  <KunCard class="p-8">
    <div class="mb-8 text-center">
      <h1 class="text-2xl font-bold text-gray-800 dark:text-white">
        欢迎回来
      </h1>
      <p class="mt-2 text-gray-600 dark:text-gray-400">
        登录 KUN OAuth 管理后台
      </p>
    </div>

    <form @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <KunInput
          v-model="email"
          label="邮箱"
          type="email"
          placeholder="请输入邮箱"
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

        <div v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-600">
          {{ error }}
        </div>

        <KunButton
          type="submit"
          color="primary"
          class="w-full"
          :disabled="isLoading"
        >
          <Icon
            v-if="isLoading"
            name="lucide:loader-2"
            class="mr-2 size-4 animate-spin"
          />
          {{ isLoading ? '登录中...' : '登录' }}
        </KunButton>
      </div>
    </form>

    <div class="mt-6 space-y-4 text-center text-sm">
      <NuxtLink
        to="/auth/forgot-password"
        class="text-indigo-600 hover:underline"
      >
        忘记密码？
      </NuxtLink>

      <p class="text-gray-600 dark:text-gray-400">
        还没有账号？
        <NuxtLink to="/auth/register" class="text-indigo-600 hover:underline">
          立即注册
        </NuxtLink>
      </p>
    </div>
  </KunCard>
</template>
