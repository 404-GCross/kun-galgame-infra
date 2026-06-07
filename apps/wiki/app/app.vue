<script setup lang="ts">
// galgame-wiki 前端,全站禁止 SEO 收录;各页面再用 useKunDisableSeo 覆盖标题
useKunDisableSeo('鲲 Galgame Wiki')

onMounted(() => {
  if (process.env.NODE_ENV === 'development') {
    // Disable pinia console info for dev
    localStorage.setItem(
      '__VUE_DEVTOOLS_NEXT_PLUGIN_SETTINGS__dev.esm.pinia__',
      '{"logStoreChanges":false}'
    )
    // Disable umami for dev
    localStorage.setItem('umami.disabled', '1')
  }
})
</script>

<template>
  <div>
    <NuxtRouteAnnouncer />
    <KunMessageProvider />
    <KunAlertProvider />
    <CommonConfirmHost />
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </div>
</template>
