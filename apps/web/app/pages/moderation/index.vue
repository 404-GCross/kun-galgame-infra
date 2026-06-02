<script setup lang="ts">
useKunDisableSeo('内容审核')
definePageMeta({
  middleware: ['auth', 'admin'],
})
</script>

<template>
  <ModerationContainer />
</template>
