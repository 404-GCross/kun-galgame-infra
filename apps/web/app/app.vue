<script setup lang="ts">
// 管理后台/内部前端,全站禁止 SEO 收录;各页面再用 useKunDisableSeo 覆盖标题
useKunDisableSeo('鲲 Galgame 管理后台')
</script>

<template>
  <div>
    <NuxtRouteAnnouncer />
    <NuxtLoadingIndicator color="var(--color-primary)" />
    <KunMessageProvider />
    <KunAlertProvider />
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </div>
</template>
