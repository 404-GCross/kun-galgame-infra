<script setup lang="ts">
useKunDisableSeo('登录')
definePageMeta({
  layout: 'auth',
  middleware: 'auth'
})
</script>

<template>
  <AuthLoginForm />
</template>
