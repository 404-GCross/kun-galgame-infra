<script setup lang="ts">
definePageMeta({
  layout: 'auth',
  middleware: 'auth'
})

const { handleCallback } = useOAuthLogin()
const route = useRoute()
const router = useRouter()

const status = ref<'processing' | 'error'>('processing')
const errorMessage = ref('')

onMounted(async () => {
  const code = route.query.code as string | undefined
  const state = route.query.state as string | undefined
  const error = route.query.error as string | undefined

  if (error) {
    status.value = 'error'
    errorMessage.value = (route.query.error_description as string) || error
    return
  }

  if (!code || !state) {
    status.value = 'error'
    errorMessage.value = '缺少 code 或 state 参数'
    return
  }

  const result = await handleCallback(code, state)
  if (result.ok) {
    router.replace('/')
  } else {
    status.value = 'error'
    errorMessage.value = result.error
  }
})
</script>

<template>
  <KunCard class="p-8 text-center">
    <template v-if="status === 'processing'">
      <Icon
        name="lucide:loader-2"
        class="text-primary mx-auto mb-4 size-10 animate-spin"
      />
      <h1 class="text-foreground text-lg font-medium">正在完成登录...</h1>
      <p class="text-default-500 mt-2 text-sm">换取 access token 中</p>
    </template>

    <template v-else>
      <Icon
        name="lucide:alert-circle"
        class="text-danger mx-auto mb-4 size-10"
      />
      <h1 class="text-foreground text-lg font-medium">登录失败</h1>
      <p class="text-default-500 mt-2 text-sm">{{ errorMessage }}</p>
      <KunButton color="primary" class="mt-6" @click="$router.push('/auth/login')">
        重新登录
      </KunButton>
    </template>
  </KunCard>
</template>
