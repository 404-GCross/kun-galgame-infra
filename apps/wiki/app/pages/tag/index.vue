<script setup lang="ts">
useKunDisableSeo('标签列表')
definePageMeta({
  middleware: 'auth'
})
</script>

<template>
  <TagList />
</template>
