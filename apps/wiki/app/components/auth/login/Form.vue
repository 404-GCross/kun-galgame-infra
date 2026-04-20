<script setup lang="ts">
const auth = useAuth()
const router = useRouter()

const account = ref('')
const password = ref('')
const error = ref('')
const isLoading = ref(false)

const handleSubmit = async () => {
  error.value = ''
  isLoading.value = true

  try {
    const response = await auth.login(account.value, password.value)
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

onMounted(() => {
  if (auth.isLoggedIn.value) {
    router.push('/')
  }
})
</script>

<template>
  <KunCard class="p-8">
    <div class="mb-8 text-center">
      <h1 class="text-foreground text-2xl font-bold">Galgame Wiki 管理</h1>
      <p class="text-default-500 mt-2">使用 KUN OAuth 账号登录</p>
    </div>

    <form @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <KunInput
          v-model="account"
          label="账号"
          type="text"
          placeholder="邮箱或用户名"
          required
          autofocus
        />

        <KunInput
          v-model="password"
          label="密码"
          type="password"
          placeholder="密码"
          required
        />

        <div
          v-if="error"
          class="bg-danger-50 text-danger rounded-lg p-3 text-sm"
        >
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
  </KunCard>
</template>
