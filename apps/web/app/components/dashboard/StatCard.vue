<script setup lang="ts">
defineProps<{
  label: string
  value: string
  icon: string
  color: string
}>()
</script>

<template>
  <KunCard :bordered="false" content-class="justify-start gap-0" class-name="rounded-xl bg-content1 p-6 shadow-sm">
    <div class="flex items-center gap-4">
      <div :class="[color, 'flex size-12 items-center justify-center rounded-lg']">
        <Icon :name="icon" class="size-6 text-white" />
      </div>
      <div>
        <p class="text-sm text-default-500">{{ label }}</p>
        <p class="text-2xl font-bold text-foreground">{{ value }}</p>
      </div>
    </div>
  </KunCard>
</template>
