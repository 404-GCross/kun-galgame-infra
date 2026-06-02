<script setup lang="ts">
useKunDisableSeo('系列列表')
definePageMeta({
  middleware: 'auth'
})
</script>

<template>
  <SeriesList />
</template>
