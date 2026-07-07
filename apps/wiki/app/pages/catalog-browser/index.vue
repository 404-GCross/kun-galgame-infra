<script setup lang="ts">
useKunDisableSeo('catalog 数据浏览器')
definePageMeta({ middleware: ['auth', 'ren'] })
</script>

<template>
  <CatalogBrowserDashboard />
</template>
