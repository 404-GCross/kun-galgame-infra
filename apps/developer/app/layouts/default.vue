<script setup lang="ts">
// Single shell for every route: sticky header + centered content + footer. The
// header shows account/控制台 per auth state. Fetch the signed-in user once on
// the server so the header renders logged-in on first paint (hydrates via
// Pinia); the client plugin restores the session when the server couldn't.
const auth = useAuth()

await callOnce('auth:user', async () => {
  if (!auth.user.value) {
    await auth.fetchUser()
  }
})
</script>

<template>
  <div class="flex min-h-screen flex-col bg-background">
    <LayoutHeader />

    <!-- min-w-0 lets wide children (tables) scroll inside instead of
         stretching this flex column past the viewport. -->
    <main class="mx-auto w-full min-w-0 max-w-7xl flex-1 px-4 py-8 md:px-6">
      <slot />
    </main>

    <footer class="border-t border-default-200">
      <div
        class="mx-auto flex max-w-7xl flex-col items-center justify-between gap-2 px-4 py-6 text-sm text-default-400 md:flex-row md:px-6"
      >
        <p>NextMoe 开放 API · 统一四源 galgame 数据</p>
        <p class="font-mono text-xs">api.nextmoe.dev/v1</p>
      </div>
    </footer>
  </div>
</template>
