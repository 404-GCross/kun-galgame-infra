<script setup lang="ts">
useKunDisableSeo('厂商列表')
definePageMeta({
  middleware: 'auth'
})
</script>

<template>
  <OfficialList />
</template>
