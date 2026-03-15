<script setup lang="ts">
defineProps<{
  label: string
  value: string
  icon: string
  color: string
}>()
</script>

<template>
  <div class="rounded-xl bg-white p-6 shadow-sm dark:bg-gray-800">
    <div class="flex items-center gap-4">
      <div :class="[color, 'flex size-12 items-center justify-center rounded-lg']">
        <Icon :name="icon" class="size-6 text-white" />
      </div>
      <div>
        <p class="text-sm text-gray-600 dark:text-gray-400">{{ label }}</p>
        <p class="text-2xl font-bold text-gray-800 dark:text-white">{{ value }}</p>
      </div>
    </div>
  </div>
</template>
