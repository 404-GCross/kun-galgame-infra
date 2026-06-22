<script setup lang="ts">
useKunDisableSeo('文件列表')
definePageMeta({ middleware: ['auth', 'admin'] })
</script>

<template>
  <ArtifactsList />
</template>
