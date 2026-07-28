<script setup lang="ts">
// Global chrome: message + alert providers for KunUI toasts/dialogs. Per-page
// SEO is set inside each page's container component.
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
    <!-- The portal's single login UI — opened from anywhere via useLoginModal. -->
    <LoginModal />
    <!-- Transient refresh-failure recovery (client-only state, so no SSR markup). -->
    <ClientOnly>
      <LayoutRefreshBanner />
    </ClientOnly>
  </div>
</template>
