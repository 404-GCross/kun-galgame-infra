<script setup lang="ts">
import type { GalgamePR } from '~/shared/types/galgame'

const props = defineProps<{
  open: boolean
  pr: GalgamePR
}>()

const emit = defineEmits<{ close: [] }>()

const api = useApi()

interface PRDiffResponse {
  pr: GalgamePR
  changed_keys: Record<string, boolean>
  base: Record<string, unknown> | null
  proposed: Record<string, unknown>
}

const data = ref<PRDiffResponse | null>(null)
const loading = ref(false)

const load = async () => {
  loading.value = true
  try {
    const response = await api.get<PRDiffResponse>(
      `/galgame/${props.pr.galgame_id}/prs/${props.pr.id}`
    )
    if (response.code === 0) data.value = response.data
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.open, props.pr.id] as const,
  ([o]) => {
    if (o) load()
  },
  { immediate: true }
)

const changedKeys = computed(() => {
  if (!data.value?.changed_keys) return []
  return Object.keys(data.value.changed_keys).filter(
    (k) => data.value!.changed_keys[k]
  )
})

const format = (v: unknown) => {
  if (v === null || v === undefined) return '—'
  if (typeof v === 'string') return v || '(空)'
  return JSON.stringify(v, null, 2)
}
</script>

<template>
  <KunModal
    :modal-value="open"
    inner-class-name="max-w-4xl"
    @update:modal-value="(v: boolean) => !v && emit('close')"
  >
    <div class="max-h-[80vh] space-y-4 overflow-y-auto p-6">
      <div>
        <h2 class="text-foreground text-lg font-semibold">
          PR #{{ pr.id }} · {{ pr.title || '(无标题)' }}
        </h2>
        <p
          v-if="pr.message"
          class="text-default-500 mt-1 whitespace-pre-wrap text-sm"
        >
          {{ pr.message }}
        </p>
      </div>

      <div v-if="loading" class="text-default-400 py-10 text-center">
        <Icon name="lucide:loader-2" class="inline size-6 animate-spin" />
      </div>

      <div v-else-if="!data" class="text-default-400 py-10 text-center">
        无数据
      </div>

      <div v-else-if="changedKeys.length === 0" class="text-default-400 py-10 text-center">
        PR 相对 base 无字段变更
      </div>

      <div v-else class="space-y-3">
        <div
          v-for="key in changedKeys"
          :key="key"
          class="border-default-200 overflow-hidden rounded-lg border"
        >
          <div
            class="bg-content2 text-default-600 px-3 py-2 font-mono text-xs"
          >
            {{ key }}
          </div>
          <div class="divide-default-200 divide-y text-sm">
            <div class="bg-danger-50/30 px-3 py-2">
              <p class="text-danger mb-1 text-xs">− base (rev {{ pr.base_revision }})</p>
              <pre class="text-foreground whitespace-pre-wrap break-words text-xs">{{
                format(data.base?.[key])
              }}</pre>
            </div>
            <div class="bg-success-50/30 px-3 py-2">
              <p class="text-success mb-1 text-xs">+ proposed</p>
              <pre class="text-foreground whitespace-pre-wrap break-words text-xs">{{
                format(data.proposed[key])
              }}</pre>
            </div>
          </div>
        </div>
      </div>

      <div class="flex justify-end pt-2">
        <KunButton variant="light" @click="emit('close')">关闭</KunButton>
      </div>
    </div>
  </KunModal>
</template>
