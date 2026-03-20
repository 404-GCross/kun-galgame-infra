<script setup lang="ts">
const auth = useAuth()

onMounted(async () => {
  if (!auth.user.value) {
    await auth.fetchUser()
  }
})
</script>

<template>
  <div class="mx-auto max-w-4xl space-y-6">
    <h1 class="text-2xl font-bold text-foreground">个人信息</h1>

    <ProfileInfo />

    <div class="grid gap-6 md:grid-cols-2">
      <ProfileEmailChange />
      <ProfilePasswordChange />
    </div>
  </div>
</template>
