<script setup lang="ts">
useKunDisableSeo('Galgame 发售月历')
definePageMeta({
  middleware: 'auth'
})
</script>

<template>
  <GalgameCalendar />
</template>
