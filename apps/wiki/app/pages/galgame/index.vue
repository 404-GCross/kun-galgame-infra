<script setup lang="ts">
useKunDisableSeo('Galgame 列表')
definePageMeta({
  middleware: 'auth'
})
</script>

<template>
  <GalgameList />
</template>
