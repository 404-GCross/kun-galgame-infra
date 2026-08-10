<script setup lang="ts">
defineProps<{ label: string; code: string; icon?: string }>()
</script>

<template>
  <div class="overflow-hidden rounded-xl border border-default-200">
    <div
      class="flex items-center justify-between gap-3 border-b border-default-200 bg-content1 px-4 py-2"
    >
      <div class="flex items-center gap-2 text-xs font-medium text-default-400">
        <KunIcon :name="icon ?? 'lucide:code'" class="size-3.5" />
        {{ label }}
      </div>
      <DocsCopyButton :text="code" label="复制配置" />
    </div>
    <pre
      class="overflow-x-auto bg-default-50 px-4 py-3 font-mono text-xs leading-relaxed text-default-600"
    ><code>{{ code }}</code></pre>
  </div>
</template>
