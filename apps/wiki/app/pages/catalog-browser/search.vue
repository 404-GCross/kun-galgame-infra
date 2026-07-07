<script setup lang="ts">
useKunDisableSeo('catalog 实体搜索')
definePageMeta({ middleware: ['auth', 'staff'] })
</script>

<template>
  <CatalogBrowserSearch />
</template>
