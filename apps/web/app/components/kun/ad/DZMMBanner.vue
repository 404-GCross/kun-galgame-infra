<script setup lang="ts">
import { kungal } from '~/config/kungal'

const props = withDefaults(
  defineProps<{
    className?: string
  }>(),
  {
    className: ''
  }
)

const { role } = usePersistUserStore()
</script>

<template>
  <KunLink
    :class-name="cn(props.className)"
    target="_blank"
    :to="kungal.ad[0]!.link"
    v-if="role < 2"
  >
    <KunImage class-name="w-full rounded-lg" :src="kungal.ad[0]!.banner" />
  </KunLink>
</template>
