<script setup lang="ts">
useKunDisableSeo('用户统计')
definePageMeta({ middleware: 'auth' })
</script>

<template>
  <UserStatsView />
</template>
