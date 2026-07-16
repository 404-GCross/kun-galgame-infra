<script setup lang="ts">
// Global chrome: message + alert providers for KunUI toasts/dialogs. Per-page
// SEO is set inside each page's container component.
useHead({
  htmlAttrs: { lang: 'zh-CN' },
  titleTemplate: (title) =>
    title ? `${title} · NextMoe 开放 API` : 'NextMoe 开放 API 开发者平台'
})
</script>

<template>
  <div>
    <NuxtRouteAnnouncer />
    <KunMessageProvider />
    <KunAlertProvider />
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </div>
</template>
