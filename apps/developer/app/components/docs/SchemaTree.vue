<script setup lang="ts">
import { cn } from '@kungal/ui-core'
import type { DocsSchemaNode } from '~~/shared/types/docs'

// Recursive schema renderer. `node` is a CONTAINER (object / map) whose fields
// this instance lists; each object/array-of-object/map field expands into a
// nested <DocsSchemaTree> for its own fields. Single root element (a <ul>) —
// required so the enclosing page transition attaches cleanly (CLAUDE.md §11).
const props = withDefaults(
  defineProps<{ node: DocsSchemaNode; depth?: number }>(),
  { depth: 0 }
)

// Label for an array element / map value.
const elementLabel = (el?: DocsSchemaNode): string => {
  if (!el) return 'any'
  if (el.type === 'array') return `${elementLabel(el.itemsOf)}[]`
  if (el.type === 'map') return 'object'
  return el.type
}

const displayType = (n: DocsSchemaNode): string => {
  if (n.type === 'array') return `${elementLabel(n.itemsOf)}[]`
  if (n.type === 'map') return `map<string, ${elementLabel(n.itemsOf)}>`
  return n.type
}

// The nested container a field expands into, or null for a leaf.
const containerOf = (n: DocsSchemaNode): DocsSchemaNode | null => {
  if (n.type === 'object' && n.children?.length) return n
  if (n.type === 'map' && n.itemsOf) return n
  if (n.type === 'array') return n.itemsOf ? containerOf(n.itemsOf) : null
  return null
}

// The field rows for a container: an object's properties, or a single synthetic
// «key» row standing for a map's value schema.
const fieldsOf = (container: DocsSchemaNode): DocsSchemaNode[] => {
  if (container.type === 'map' && container.itemsOf) {
    return [{ ...container.itemsOf, name: '«key»' }]
  }
  return container.children ?? []
}

const rows = computed(() =>
  fieldsOf(props.node).map((field) => ({
    field,
    type: displayType(field),
    container: containerOf(field)
  }))
)

// Auto-expand only the first level (the envelope's `data`); deeper objects open
// on click so a large record stays scannable.
const open = reactive<Record<number, boolean>>({})
rows.value.forEach((r, i) => {
  if (r.container) open[i] = props.depth < 1
})
const toggle = (i: number) => {
  open[i] = !open[i]
}
</script>

<template>
  <ul class="space-y-1">
    <li v-for="(row, i) in rows" :key="row.field.name ?? i">
      <div class="flex items-start gap-2 py-1">
        <button
          v-if="row.container"
          type="button"
          class="mt-0.5 flex size-4 shrink-0 items-center justify-center rounded text-default-400 transition-colors hover:text-primary"
          :aria-expanded="open[i]"
          :aria-label="open[i] ? '折叠' : '展开'"
          @click="toggle(i)"
        >
          <KunIcon
            :name="open[i] ? 'lucide:chevron-down' : 'lucide:chevron-right'"
            class="size-4"
          />
        </button>
        <span v-else class="mt-0.5 size-4 shrink-0" aria-hidden="true" />

        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
            <code class="font-mono text-sm font-medium text-foreground">
              {{ row.field.name }}
            </code>
            <code class="font-mono text-xs text-default-400">{{ row.type }}</code>
            <span
              v-if="row.field.format"
              class="rounded bg-default-100 px-1 py-px font-mono text-[0.625rem] text-default-500"
            >
              {{ row.field.format }}
            </span>
            <span
              v-if="row.field.required"
              class="text-[0.625rem] font-semibold uppercase tracking-wide text-danger-600"
            >
              required
            </span>
            <span
              v-if="row.field.nullable"
              class="text-[0.625rem] font-medium uppercase tracking-wide text-default-300"
            >
              nullable
            </span>
          </div>

          <p
            v-if="row.field.doc"
            class="mt-0.5 text-sm leading-relaxed text-default-500"
          >
            {{ row.field.doc }}
          </p>

          <div v-if="row.field.enum" class="mt-1 flex flex-wrap items-center gap-1">
            <span class="text-xs text-default-400">枚举</span>
            <code
              v-for="v in row.field.enum"
              :key="v"
              class="rounded bg-default-100 px-1.5 py-px font-mono text-xs text-default-600"
            >
              {{ v }}
            </code>
          </div>

          <DocsSchemaTree
            v-if="row.container && open[i]"
            :node="row.container"
            :depth="depth + 1"
            :class="
              cn('mt-1 border-l border-default-200 pl-3', depth > 4 && 'pl-2')
            "
          />
        </div>
      </div>
    </li>
  </ul>
</template>
