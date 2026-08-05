<script setup lang="ts">
useKunDisableSeo('权限矩阵')
definePageMeta({ middleware: ['auth', 'admin'] })
</script>

<template>
  <AdminPermissionContainer />
</template>
