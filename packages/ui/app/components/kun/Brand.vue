<script setup lang="ts">
// KunBrand is the home-link header logo + name + optional sub-label
// (e.g. "论坛" / "Wiki" / "OAuth"). All values must be passed in by the
// consuming app — v0.0.1 hard-coded `kungal.titleShort` + a "论坛" chip
// which made the component kungal-only and not safe for the @kun/ui
// cross-project layer. See packages/ui/docs/improvement-plan.md §3.19.

withDefaults(
  defineProps<{
    name: string
    iconSrc?: string
    iconAlt?: string
    iconClass?: string
    badge?: string
    badgeColor?: import('./ui/type').KunUIColor
    to?: string
    nameClass?: string
  }>(),
  {
    iconSrc: '/favicon.webp',
    iconAlt: 'logo',
    iconClass: 'size-10 rounded-2xl',
    badge: '',
    badgeColor: 'primary',
    to: '/',
    nameClass: 'text-xl'
  }
)
</script>

<template>
  <div class="flex cursor-pointer items-center gap-3" @click="navigateTo(to)">
    <KunImage
      :class="iconClass"
      :src="iconSrc"
      :alt="iconAlt"
    />
    <span :class="nameClass">{{ name }}</span>
    <KunChip v-if="badge" size="md" :color="badgeColor">{{ badge }}</KunChip>
  </div>
</template>
