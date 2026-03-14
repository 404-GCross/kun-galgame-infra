<script setup lang="ts">
definePageMeta({
  layout: 'auth'
})

const auth = useAuth()
const router = useRouter()

const name = ref('')
const email = ref('')
const password = ref('')
const confirmPassword = ref('')
const error = ref('')
const isLoading = ref(false)

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
    const response = await auth.register(
      name.value,
      email.value,
      password.value
    )
    if (response.code === 0) {
      router.push('/auth/login?registered=true')
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
      <h1 class="text-2xl font-bold text-gray-800 dark:text-white">
        创建账号
      </h1>
      <p class="mt-2 text-gray-600 dark:text-gray-400">
        加入 KUN Visual Novel 社区
      </p>
    </div>

    <form @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <KunInput
          v-model="name"
          label="用户名"
          type="text"
          placeholder="请输入用户名"
          required
          autofocus
        />

        <KunInput
          v-model="email"
          label="邮箱"
          type="email"
          placeholder="请输入邮箱"
          required
        />

        <KunInput
          v-model="password"
          label="密码"
          type="password"
          placeholder="请输入密码"
          required
        />

        <KunInput
          v-model="confirmPassword"
          label="确认密码"
          type="password"
          placeholder="请再次输入密码"
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
          {{ isLoading ? '注册中...' : '注册' }}
        </KunButton>
      </div>
    </form>

    <div class="mt-6 text-center text-sm">
      <p class="text-gray-600 dark:text-gray-400">
        已有账号？
        <NuxtLink to="/auth/login" class="text-indigo-600 hover:underline">
          立即登录
        </NuxtLink>
      </p>
    </div>
  </KunCard>
</template>
