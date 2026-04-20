<script setup lang="ts">
const { login } = useOAuthLogin()
const auth = useAuth()
const router = useRouter()

const isLoading = ref(false)

const handleSubmit = async () => {
  isLoading.value = true
  try {
    await login()
  } catch {
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
      <p class="text-default-500 mt-2">使用 KUN 账号授权登录</p>
    </div>

    <KunButton
      color="primary"
      class="w-full"
      :disabled="isLoading"
      @click="handleSubmit"
    >
      <Icon
        v-if="isLoading"
        name="lucide:loader-2"
        class="mr-2 size-4 animate-spin"
      />
      <Icon v-else name="lucide:key-round" class="mr-2 size-4" />
      {{ isLoading ? '跳转中...' : '用 KUN OAuth 登录' }}
    </KunButton>

    <p class="text-default-400 mt-6 text-center text-xs">
      点击后会跳转到 KUN OAuth 登录页进行授权
    </p>
  </KunCard>
</template>
