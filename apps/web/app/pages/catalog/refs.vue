<script setup lang="ts">
useKunDisableSeo('目录人审 · 外部链接确认')
definePageMeta({ middleware: ['auth', 'admin'] })
</script>

<template>
  <CatalogRefs />
</template>
