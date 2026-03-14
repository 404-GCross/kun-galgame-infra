<script setup lang="ts">
definePageMeta({
  layout: 'auth'
})

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
      error.value = response.message || 'Failed to send reset email'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : 'Failed to send reset email'
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunCard class="p-8">
    <div class="mb-8 text-center">
      <h1 class="text-2xl font-bold text-gray-800 dark:text-white">
        Reset Password
      </h1>
      <p class="mt-2 text-gray-600 dark:text-gray-400">
        Enter your email to receive a reset link
      </p>
    </div>

    <div v-if="success" class="text-center">
      <div
        class="mb-4 inline-flex size-16 items-center justify-center rounded-full bg-green-100"
      >
        <Icon name="lucide:check" class="size-8 text-green-600" />
      </div>
      <h2 class="mb-2 text-lg font-semibold text-gray-800 dark:text-white">
        Check Your Email
      </h2>
      <p class="mb-6 text-gray-600 dark:text-gray-400">
        If an account exists with this email, we've sent a password reset link.
      </p>
      <NuxtLink to="/auth/login" class="text-indigo-600 hover:underline">
        Back to login
      </NuxtLink>
    </div>

    <form v-else @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <KunInput
          v-model="email"
          label="Email"
          type="email"
          placeholder="Enter your email"
          required
          autofocus
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
          {{ isLoading ? 'Sending...' : 'Send Reset Link' }}
        </KunButton>
      </div>
    </form>

    <div v-if="!success" class="mt-6 text-center text-sm">
      <NuxtLink to="/auth/login" class="text-indigo-600 hover:underline">
        Back to login
      </NuxtLink>
    </div>
  </KunCard>
</template>
