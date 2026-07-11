<script setup lang="ts">
// The T&S registries page: two never-delete registries side by side. Rows are
// only ever created, edited, or deprecated (PATCH is_deprecated) — deletion is
// deliberately absent from both the UI and the backend contract.
</script>

<template>
  <div class="space-y-4">
    <h1 class="text-foreground text-2xl font-bold">T&S 审核队列</h1>
    <TrustSubNav />
    <div class="grid grid-cols-1 gap-4 xl:grid-cols-2">
      <TrustSubjectKinds />
      <TrustReasons />
    </div>
  </div>
</template>
