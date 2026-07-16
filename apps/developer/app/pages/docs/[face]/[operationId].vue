<script setup lang="ts">
// Single operation page — route definition only. Keyed by path so navigating
// between operations remounts + runs the content transition (sidebar stays put).
definePageMeta({ layout: 'docs', key: (route) => route.path })
</script>

<template>
  <DocsOperation />
</template>
