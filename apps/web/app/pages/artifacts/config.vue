<script setup lang="ts">
useKunDisableSeo('存储配置')
definePageMeta({ middleware: ['auth', 'admin'] })
</script>

<template>
  <ArtifactsConfig />
</template>
