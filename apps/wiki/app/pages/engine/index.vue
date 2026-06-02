<script setup lang="ts">
useKunDisableSeo('引擎列表')
definePageMeta({
  middleware: 'auth'
})
</script>

<template>
  <EngineList />
</template>
