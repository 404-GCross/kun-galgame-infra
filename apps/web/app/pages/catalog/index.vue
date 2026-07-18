<script setup lang="ts">
// Bare /catalog has no view of its own — the nav only links the sub-routes
// (/catalog/candidates|proposals|refs), so hitting /catalog directly would 404.
// Redirect to the default sub-page. 302 + replace keeps this stub out of the
// browser history so Back doesn't bounce through it.
// Match the sub-pages' gate (['auth','ren']) so the redirect never applies a
// different access rule than its target.
definePageMeta({ middleware: ['auth', 'ren'] })

await navigateTo('/catalog/candidates', { redirectCode: 302, replace: true })
</script>

<template>
  <div />
</template>
