<script setup lang="ts">
useKunDisableSeo('审核列表')
definePageMeta({
  middleware: 'auth'
})
</script>

<template>
  <ReviewContainer />
</template>
