<script setup lang="ts">
definePageMeta({
  middleware: ['auth', 'admin'],
})
</script>

<template>
  <SitesContainer />
</template>
