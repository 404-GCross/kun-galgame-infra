<script setup lang="ts">
// Recursive JSON tree for the /explore data browser. Objects/arrays render as
// collapsible nodes (open by default down to depth 2); primitives render as
// typed leaves — string/number/boolean/null each in a palette color. Pure
// display: the data is whatever the API returned, never reshaped.
const props = withDefaults(
  defineProps<{ data: unknown; name?: string; depth?: number }>(),
  { name: '', depth: 0 }
)

const open = ref(props.depth < 2)

const isObj = computed(
  () =>
    props.data !== null &&
    typeof props.data === 'object' &&
    !Array.isArray(props.data)
)
const isArr = computed(() => Array.isArray(props.data))

const entries = computed<(readonly [string, unknown])[]>(() => {
  if (isArr.value)
    return (props.data as unknown[]).map((v, i) => [String(i), v] as const)
  if (isObj.value) return Object.entries(props.data as Record<string, unknown>)
  return []
})

// Interpolated in the template as a computed: a `{${…}}` template literal
// inline in {{ }} would collide with the mustache terminator.
const sizeBadge = computed(() =>
  isArr.value ? `[${entries.value.length}]` : `{${entries.value.length}}`
)

const leafClass = computed(() => {
  const v = props.data
  if (typeof v === 'string') return 'text-success'
  if (typeof v === 'number') return 'text-primary'
  if (typeof v === 'boolean') return 'text-warning'
  return 'text-default-400'
})

const leafText = computed(() =>
  typeof props.data === 'string' ? JSON.stringify(props.data) : String(props.data)
)
</script>

<template>
  <div class="font-mono text-xs leading-relaxed">
    <template v-if="isObj || isArr">
      <button
        type="button"
        class="flex items-center gap-1 rounded px-0.5 text-left hover:bg-default-100"
        @click="open = !open"
      >
        <KunIcon
          :name="open ? 'lucide:chevron-down' : 'lucide:chevron-right'"
          class="size-3 shrink-0 text-default-400"
        />
        <span v-if="name" class="text-default-500">{{ name }}</span>
        <span class="text-default-400">{{ sizeBadge }}</span>
      </button>
      <div v-show="open" class="ml-1.5 border-l border-default-200 pl-3">
        <JsonTree
          v-for="[k, v] in entries"
          :key="k"
          :data="v"
          :name="k"
          :depth="depth + 1"
        />
      </div>
    </template>
    <div v-else class="flex gap-1 px-0.5">
      <span v-if="name" class="shrink-0 text-default-500">{{ name }}:</span>
      <span class="break-all" :class="leafClass">{{ leafText }}</span>
    </div>
  </div>
</template>
