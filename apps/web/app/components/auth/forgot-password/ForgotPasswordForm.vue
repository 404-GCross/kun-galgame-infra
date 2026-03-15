<script setup lang="ts">
const auth = useAuth()

const email = ref('')
const error = ref('')
const success = ref(false)
const isLoading = ref(false)

const handleSubmit = async () => {
  error.value = ''
  isLoading.value = true

  try {
    const response = await auth.forgotPassword(email.value)
    if (response.code === 0) {
      success.value = true
    } else {
      error.value = response.message || '发送失败'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '发送失败'
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunCard class="p-8">
    <div class="mb-8 text-center">
      <h1 class="text-2xl font-bold text-gray-800 dark:text-white">重置密码</h1>
      <p class="mt-2 text-gray-600 dark:text-gray-400">输入您的邮箱以接收重置链接</p>
    </div>

    <div v-if="success" class="text-center">
      <div class="mb-4 inline-flex size-16 items-center justify-center rounded-full bg-green-100">
        <Icon name="lucide:check" class="size-8 text-green-600" />
      </div>
      <h2 class="mb-2 text-lg font-semibold text-gray-800 dark:text-white">请检查您的邮箱</h2>
      <p class="mb-6 text-gray-600 dark:text-gray-400">如果该邮箱已注册，我们已发送密码重置链接。</p>
      <NuxtLink to="/auth/login" class="text-indigo-600 hover:underline">返回登录</NuxtLink>
    </div>

    <form v-else @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <KunInput v-model="email" label="邮箱" type="email" placeholder="请输入邮箱" required autofocus />

        <div v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-600">{{ error }}</div>

        <KunButton type="submit" color="primary" class="w-full" :disabled="isLoading">
          <Icon v-if="isLoading" name="lucide:loader-2" class="mr-2 size-4 animate-spin" />
          {{ isLoading ? '发送中...' : '发送重置链接' }}
        </KunButton>
      </div>
    </form>

    <div v-if="!success" class="mt-6 text-center text-sm">
      <NuxtLink to="/auth/login" class="text-indigo-600 hover:underline">返回登录</NuxtLink>
    </div>
  </KunCard>
</template>
