<script setup lang="ts">
useKunDisableSeo('catalog 作品详情')
definePageMeta({ middleware: ['auth', 'staff'] })
</script>

<template>
  <CatalogBrowserWork />
</template>
