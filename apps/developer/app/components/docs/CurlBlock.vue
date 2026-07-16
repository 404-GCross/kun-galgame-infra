<script setup lang="ts">
// A generated curl example with a copy affordance. The dark terminal-style
// surface is a fixed solid color (content2), NOT theme-inverted, so a request
// example reads like a shell everywhere; wide lines scroll inside the block.
defineProps<{ code: string }>()
</script>

<template>
  <div class="overflow-hidden rounded-xl border border-default-200">
    <div
      class="flex items-center justify-between gap-3 border-b border-default-200 bg-content1 px-4 py-2"
    >
      <div class="flex items-center gap-2 text-xs font-medium text-default-400">
        <KunIcon name="lucide:terminal" class="size-3.5" />
        cURL
      </div>
      <DocsCopyButton :text="code" label="复制 curl 示例" />
    </div>
    <pre
      class="overflow-x-auto bg-default-50 px-4 py-3 font-mono text-xs leading-relaxed text-default-600"
    ><code>{{ code }}</code></pre>
  </div>
</template>
