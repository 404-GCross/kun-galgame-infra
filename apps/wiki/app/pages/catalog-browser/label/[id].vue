<script setup lang="ts">
useKunDisableSeo('catalog 厂牌')
definePageMeta({ middleware: ['auth', 'ren'] })
</script>

<template>
  <CatalogBrowserLabel />
</template>
