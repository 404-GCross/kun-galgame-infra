<script setup lang="ts">
useKunDisableSeo('注册')
definePageMeta({
  layout: 'auth',
})
</script>

<template>
  <AuthRegisterForm />
  <!-- TODO: re-enable the "一键登录以下网站" ecosystem strip once everything
       (downstream CORS, prod client `listed` flags, logos) is ready. Backend
       (GET /oauth/ecosystem) stays live; this just hides the UI for now.
       Component kept at components/auth/EcosystemStrip.vue. -->
  <!-- <AuthEcosystemStrip /> -->
</template>
