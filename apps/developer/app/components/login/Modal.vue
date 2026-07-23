<script setup lang="ts">
// The portal's single login UI. Opened via useLoginModal from the header CTA,
// the landing CTA, and the /login fallback route. Logs in with the NextMoe
// (未萌 / 鲲 Galgame) ecosystem account through the same-origin relay
// (useAuth.login), then lands on the console (or the stored redirect).
const auth = useAuth()
const { isOpen, redirect, close } = useLoginModal()

const account = ref('')
const password = ref('')
const error = ref('')
const isLoading = ref(false)

const reset = () => {
  account.value = ''
  password.value = ''
  error.value = ''
  isLoading.value = false
}

// Wipe transient input/error state whenever the modal is dismissed.
watch(isOpen, (open) => {
  if (!open) reset()
})

const handleSubmit = async () => {
  if (isLoading.value) return
  error.value = ''
  isLoading.value = true
  try {
    const response = await auth.login(account.value, password.value)
    if (response.code !== 0) {
      error.value = response.message || '登录失败'
      return
    }
    const to = redirect.value || '/dashboard'
    close()
    await navigateTo(to)
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '登录失败'
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunModal v-model="isOpen" size="md">
    <div class="space-y-6">
      <div class="space-y-1 text-center">
        <div
          class="mx-auto flex size-11 items-center justify-center rounded-xl bg-primary text-white"
        >
          <KunIcon name="lucide:boxes" class="size-6" />
        </div>
        <h2 class="pt-2 text-xl font-bold text-foreground">登录开发者平台</h2>
        <p class="text-sm text-default-500">
          使用你的 NextMoe（未萌）账号登录
        </p>
      </div>

      <form class="space-y-4" @submit.prevent="handleSubmit">
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
          {{ isLoading ? '登录中…' : '登录' }}
        </KunButton>
      </form>

      <p class="text-center text-sm text-default-500">
        还没有账号？前往
        <a
          href="https://www.kungal.com"
          target="_blank"
          rel="noopener noreferrer"
          class="text-primary hover:underline"
        >
          NextMoe（鲲 Galgame）
        </a>
        注册。
      </p>
    </div>
  </KunModal>
</template>
