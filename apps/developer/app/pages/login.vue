<script setup lang="ts">
const { open } = useLoginModal()
const route = useRoute()

const accessToken = useCookie('access_token')

onMounted(() => {
  if (accessToken.value) {
    navigateTo('/dashboard')
    return
  }
  open(route.query.redirect as string | undefined)
})
</script>

<template>
  <LandingContainer />
</template>
