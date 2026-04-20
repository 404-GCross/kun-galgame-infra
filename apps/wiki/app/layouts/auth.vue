<script setup lang="ts">
const colorMode = useColorMode()

const colorModeIcon = computed(() => {
  if (colorMode.preference === 'system') return 'lucide:monitor'
  return colorMode.value === 'dark' ? 'lucide:moon' : 'lucide:sun'
})

const colorModeOptions = [
  { value: 'light', label: '浅色', icon: 'lucide:sun' },
  { value: 'dark', label: '深色', icon: 'lucide:moon' },
  { value: 'system', label: '跟随系统', icon: 'lucide:monitor' }
] as const

const setColorMode = (mode: string) => {
  colorMode.preference = mode
}
</script>

<template>
  <div
    class="bg-default-50 relative flex min-h-screen items-center justify-center p-4"
  >
    <div class="absolute top-4 right-4">
      <KunPopover position="bottom-end">
        <template #trigger>
          <button
            class="text-default-400 hover:bg-default-100 hover:text-foreground rounded-lg p-2 transition-colors"
            title="切换主题"
          >
            <Icon :name="colorModeIcon" class="size-5" />
          </button>
        </template>

        <div class="w-36 py-1">
          <button
            v-for="option in colorModeOptions"
            :key="option.value"
            class="flex w-full items-center gap-3 px-3 py-2 text-sm transition-colors"
            :class="
              colorMode.preference === option.value
                ? 'bg-primary-50 text-primary'
                : 'text-default-500 hover:bg-default-100 hover:text-foreground'
            "
            @click="setColorMode(option.value)"
          >
            <Icon :name="option.icon" class="size-4" />
            <span>{{ option.label }}</span>
          </button>
        </div>
      </KunPopover>
    </div>

    <div class="w-full max-w-md">
      <slot />
    </div>
  </div>
</template>
