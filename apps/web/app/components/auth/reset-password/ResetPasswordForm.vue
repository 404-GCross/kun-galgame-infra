<script setup lang="ts">
const auth = useAuth()
const route = useRoute()
const router = useRouter()

const token = computed(() => route.query.token as string)
const password = ref('')
const confirmPassword = ref('')
const error = ref('')
const success = ref(false)
const isLoading = ref(false)

const handleSubmit = async () => {
  error.value = ''

  if (!token.value) {
    error.value = '无效的重置链接'
    return
  }
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
    const response = await auth.resetPassword(token.value, password.value)
    if (response.code === 0) {
      success.value = true
      setTimeout(() => router.push('/auth/login'), 3000)
    } else {
      error.value = response.message || '重置失败'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '重置失败'
  } finally {
    isLoading.value = false
  }
}

onMounted(() => {
  if (!token.value) {
    error.value = '无效的重置链接，请重新申请。'
  }
})
</script>

<template>
  <KunCard class="p-8">
    <div class="mb-8 text-center">
      <h1 class="text-2xl font-bold text-foreground">设置新密码</h1>
      <p class="mt-2 text-default-500">请输入您的新密码</p>
    </div>

    <div v-if="success" class="text-center">
      <div class="mb-4 inline-flex size-16 items-center justify-center rounded-full bg-success-100">
        <Icon name="lucide:check" class="size-8 text-success" />
      </div>
      <h2 class="mb-2 text-lg font-semibold text-foreground">密码重置成功</h2>
      <p class="mb-6 text-default-500">您的密码已重置，正在跳转到登录页面...</p>
      <NuxtLink to="/auth/login" class="text-primary hover:underline">立即登录</NuxtLink>
    </div>

    <form v-else @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <KunInput v-model="password" label="新密码" type="password" placeholder="请输入新密码" required autofocus />
        <KunInput v-model="confirmPassword" label="确认密码" type="password" placeholder="请再次输入新密码" required />

        <div v-if="error" class="rounded-lg bg-danger-50 p-3 text-sm text-danger">{{ error }}</div>

        <KunButton type="submit" color="primary" class="w-full" :disabled="isLoading || !token">
          <Icon v-if="isLoading" name="lucide:loader-2" class="mr-2 size-4 animate-spin" />
          {{ isLoading ? '重置中...' : '重置密码' }}
        </KunButton>
      </div>
    </form>

    <div v-if="!success" class="mt-6 text-center text-sm">
      <NuxtLink to="/auth/forgot-password" class="text-primary hover:underline">重新申请重置链接</NuxtLink>
    </div>
  </KunCard>
</template>
