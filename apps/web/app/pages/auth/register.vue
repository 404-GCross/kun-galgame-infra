<script setup lang="ts">
definePageMeta({
  layout: 'auth',
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
    error.value = 'Passwords do not match'
    return
  }

  if (password.value.length < 6) {
    error.value = 'Password must be at least 6 characters'
    return
  }

  isLoading.value = true

  try {
    const response = await auth.register(name.value, email.value, password.value)
    if (response.code === 0) {
      router.push('/auth/login?registered=true')
    } else {
      error.value = response.message || 'Registration failed'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : 'Registration failed'
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunCardCard class="p-8">
    <div class="mb-8 text-center">
      <h1 class="text-2xl font-bold text-gray-800 dark:text-white">
        Create Account
      </h1>
      <p class="mt-2 text-gray-600 dark:text-gray-400">
        Join KUN Visual Novel community
      </p>
    </div>

    <form @submit.prevent="handleSubmit">
      <div class="space-y-4">
        <KunInput
          v-model="name"
          label="Username"
          type="text"
          placeholder="Choose a username"
          required
          autofocus
        />

        <KunInput
          v-model="email"
          label="Email"
          type="email"
          placeholder="Enter your email"
          required
        />

        <KunInput
          v-model="password"
          label="Password"
          type="password"
          placeholder="Create a password"
          required
        />

        <KunInput
          v-model="confirmPassword"
          label="Confirm Password"
          type="password"
          placeholder="Confirm your password"
          required
        />

        <div v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-600">
          {{ error }}
        </div>

        <KunButtonButton
          type="submit"
          color="primary"
          class="w-full"
          :disabled="isLoading"
        >
          <Icon v-if="isLoading" name="lucide:loader-2" class="mr-2 size-4 animate-spin" />
          {{ isLoading ? 'Creating account...' : 'Create Account' }}
        </KunButtonButton>
      </div>
    </form>

    <div class="mt-6 text-center text-sm">
      <p class="text-gray-600 dark:text-gray-400">
        Already have an account?
        <NuxtLink to="/auth/login" class="text-indigo-600 hover:underline">
          Sign in
        </NuxtLink>
      </p>
    </div>
  </KunCardCard>
</template>
