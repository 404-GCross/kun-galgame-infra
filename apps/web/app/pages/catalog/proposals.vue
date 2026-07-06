<script setup lang="ts">
useKunDisableSeo('目录人审 · 合并提案')
definePageMeta({ middleware: ['auth', 'admin'] })
</script>

<template>
  <CatalogProposals />
</template>
