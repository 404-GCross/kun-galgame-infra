<script setup lang="ts">
useKunDisableSeo('目录人审 · 候选')
definePageMeta({ middleware: ['auth', 'admin'] })
</script>

<template>
  <CatalogCandidates />
</template>
