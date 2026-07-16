<script setup lang="ts">
import type { DocsParam } from '~~/shared/types/docs'

// Parameter reference table (path params first, then query — pre-sorted in the
// model). Wide content scrolls inside its own container so the page body never
// scrolls sideways (CLAUDE.md §12).
defineProps<{ params: DocsParam[] }>()

const inLabel: Record<string, string> = { path: 'path', query: 'query' }
</script>

<template>
  <div class="overflow-x-auto rounded-xl border border-default-200">
    <table class="w-full min-w-[36rem] text-sm">
      <thead>
        <tr class="border-b border-default-200 text-left text-default-400">
          <th class="px-4 py-2 font-medium">参数</th>
          <th class="px-4 py-2 font-medium">类型</th>
          <th class="px-4 py-2 font-medium">位置</th>
          <th class="px-4 py-2 font-medium">说明</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="(p, i) in params"
          :key="`${p.in}-${p.name}`"
          class="border-b border-default-100 align-top"
          :class="i === params.length - 1 && 'border-b-0'"
        >
          <td class="px-4 py-3 whitespace-nowrap">
            <code class="font-mono font-medium text-foreground">{{ p.name }}</code>
            <span
              v-if="p.required"
              class="ml-1.5 text-[0.625rem] font-semibold uppercase tracking-wide text-danger-600"
            >
              必填
            </span>
          </td>
          <td class="px-4 py-3 whitespace-nowrap">
            <code class="font-mono text-xs text-default-500">{{ p.type }}</code>
            <code v-if="p.format" class="ml-1 font-mono text-xs text-default-300">
              {{ p.format }}
            </code>
          </td>
          <td class="px-4 py-3">
            <code class="font-mono text-xs text-default-400">
              {{ inLabel[p.in] ?? p.in }}
            </code>
          </td>
          <td class="px-4 py-3 text-default-500">
            <p v-if="p.doc" class="leading-relaxed">{{ p.doc }}</p>
            <div v-if="p.enum" class="mt-1 flex flex-wrap items-center gap-1">
              <code
                v-for="v in p.enum"
                :key="v"
                class="rounded bg-default-100 px-1.5 py-px font-mono text-xs text-default-600"
              >
                {{ v }}
              </code>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
