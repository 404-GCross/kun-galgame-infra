<script setup lang="ts">
import { kungal } from '~/config/kungal'

const { role } = usePersistUserStore()
</script>

<template>
  <KunTooltip
    class-name="flex"
    text="听说现在的 AI 比人还要 H ?"
    position="bottom"
    v-if="role < 2"
  >
    <KunLink target="_blank" :to="kungal.ad[0]!.link">
      <KunImage class-name="hidden md:block h-11" :src="kungal.ad[0]!.icon" />
    </KunLink>
  </KunTooltip>
</template>
