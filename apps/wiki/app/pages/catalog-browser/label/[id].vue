<script setup lang="ts">
useKunDisableSeo('catalog 厂牌')
definePageMeta({ middleware: ['auth', 'staff'] })
</script>

<template>
  <CatalogBrowserLabel />
</template>
