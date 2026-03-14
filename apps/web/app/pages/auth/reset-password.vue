<script setup lang="ts">
definePageMeta({
  layout: 'auth'
})

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
    error.value = 'Invalid reset link'
    return
  }

  if (password.value !== confirmPassword.value) {
    error.value = 'Passwords do not match'
    return
  }

  if (password.value.length < 6) {
    error.value = 'Password must be at least 6 characters'
    return
  }

  isLoading.value = true

  try {
    const response = await auth.resetPassword(token.value, password.value)
    if (response.code === 0) {
      success.value = true
      setTimeout(() => {
        router.push('/auth/login')
      }, 3000)
    } else {
      error.value = response.message || 'Failed to reset password'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : 'Failed to reset password'
  } finally {
    isLoading.value = false
  }
}

onMounted(() => {
  if (!token.value) {
    error.value = 'Invalid reset link. Please request a new one.'
  }
})
</script>

<template>
  <KunCard class="p-8">
    <div class="mb-8 text-center">
      <h1 class="text-2xl font-bold text-gray-800 dark:text-white">
        Set New Password
      </h1>
      <p class="mt-2 text-gray-600 dark:text-gray-400">
        Enter your new password
      </p>
    </div>

    <div v-if="success" class="text-center">
      <div
        class="mb-4 inline-flex size-16 items-center justify-center rounded-full bg-green-100"
      >
        <Icon name="lucide:check" class="size-8 text-green-600" />
      </div>
      <h2 class="mb-2 text-lg font-semibold text-gray-800 dark:text-white">
        Password Reset Successful
      </h2>
      <p class="mb-6 text-gray-600 dark:text-gray-400">
        Your password has been reset. Redirecting to login...
      </p>
      <NuxtLink to="/auth/login" class="text-indigo-600 hover:underline">
        Go to login now
      </NuxtLink>
    </div>

    <form v-else @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <KunInput
          v-model="password"
          label="New Password"
          type="password"
          placeholder="Enter new password"
          required
          autofocus
        />

        <KunInput
          v-model="confirmPassword"
          label="Confirm Password"
          type="password"
          placeholder="Confirm new password"
          required
        />

        <div v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-600">
          {{ error }}
        </div>

        <KunButton
          type="submit"
          color="primary"
          class="w-full"
          :disabled="isLoading || !token"
        >
          <Icon
            v-if="isLoading"
            name="lucide:loader-2"
            class="mr-2 size-4 animate-spin"
          />
          {{ isLoading ? 'Resetting...' : 'Reset Password' }}
        </KunButton>
      </div>
    </form>

    <div v-if="!success" class="mt-6 text-center text-sm">
      <NuxtLink
        to="/auth/forgot-password"
        class="text-indigo-600 hover:underline"
      >
        Request new reset link
      </NuxtLink>
    </div>
  </KunCard>
</template>
