<script setup lang="ts">
// Face overview — route definition only. Keyed by path so switching faces
// remounts + runs the content transition (the layout's sidebar stays put).
definePageMeta({ layout: 'docs', key: (route) => route.path })
</script>

<template>
  <DocsFace />
</template>
