<script setup lang="ts">
useHead({
  htmlAttrs: { lang: 'zh-CN' },
  titleTemplate: (title) =>
    title ? `${title} · NextMoe 开发者平台` : 'NextMoe 开发者平台 · ACGN 数据，以此为准'
})
</script>

<template>
  <div>
    <NuxtLoadingIndicator color="var(--color-primary)" />
    <NuxtRouteAnnouncer />
    <KunMessageProvider />
    <KunAlertProvider />
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
    <LoginModal />
    <ClientOnly>
      <LayoutRefreshBanner />
    </ClientOnly>
  </div>
</template>
