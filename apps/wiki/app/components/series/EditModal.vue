<script setup lang="ts">
import type { Galgame, GalgameSeries } from '~/shared/types/galgame'

// Used for both create (series=null) and edit (series=given).
const props = defineProps<{
  open: boolean
  series?: (GalgameSeries & { galgame?: Galgame[] }) | null
}>()

const emit = defineEmits<{
  close: []
  saved: [id?: number]
}>()

const api = useApi()

const form = ref({
  name: '',
  description: ''
})

// Selected galgames for assignment
const pickedGalgames = ref<Galgame[]>([])

// Search
const searchQuery = ref('')
const searchResults = ref<Galgame[]>([])
const searching = ref(false)

const isEdit = computed(() => !!props.series?.id)

const pickedIds = computed(() => pickedGalgames.value.map((g) => g.id))

const displayName = (g: Galgame) =>
  g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw || '(无标题)'

watch(
  () => props.open,
  (v) => {
    if (v) {
      form.value = {
        name: props.series?.name ?? '',
        description: props.series?.description ?? ''
      }
      pickedGalgames.value = props.series?.galgame
        ? [...props.series.galgame]
        : []
      searchQuery.value = ''
      searchResults.value = []
    }
  },
  { immediate: true }
)

let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(searchQuery, (q) => {
  if (searchTimer) clearTimeout(searchTimer)
  const keywords = q.trim()
  if (!keywords) {
    searchResults.value = []
    return
  }
  searchTimer = setTimeout(async () => {
    searching.value = true
    try {
      const response = await api.get<Galgame[]>('/series/search', { keywords })
      if (response.code === 0) searchResults.value = response.data ?? []
    } finally {
      searching.value = false
    }
  }, 300)
})

const add = (g: Galgame) => {
  if (pickedIds.value.includes(g.id)) return
  pickedGalgames.value = [...pickedGalgames.value, g]
}

const remove = (id: number) => {
  pickedGalgames.value = pickedGalgames.value.filter((g) => g.id !== id)
}

const submitting = ref(false)

const submit = async () => {
  submitting.value = true
  try {
    const payload = {
      name: form.value.name,
      description: form.value.description,
      galgame_ids: pickedIds.value
    }

    const response = isEdit.value
      ? await api.put<{ id: number }>(`/series/${props.series!.id}`, payload)
      : await api.post<{ id: number }>('/series', payload)

    if (response.code === 0) {
      useKunMessage(isEdit.value ? '保存成功' : '创建成功', 'success')
      emit('saved', response.data?.id)
    } else {
      useKunMessage(response.message || '保存失败', 'error')
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <KunModal
    :model-value="open"
    inner-class-name="max-w-2xl"
    @update:model-value="(v: boolean) => !v && emit('close')"
  >
    <div class="max-h-[80vh] space-y-4 overflow-y-auto p-6">
      <h2 class="text-foreground text-lg font-semibold">
        {{ isEdit ? '编辑系列' : '创建系列' }}
      </h2>

      <KunInput v-model="form.name" label="名称" required />
      <KunTextarea v-model="form.description" label="描述" :rows="3" />

      <div>
        <span class="text-default-600 mb-2 block text-sm">
          包含的 galgame ({{ pickedGalgames.length }})
        </span>

        <div class="mb-2 flex flex-wrap gap-2">
          <span
            v-for="g in pickedGalgames"
            :key="g.id"
            class="bg-primary-50 text-primary flex items-center gap-1 rounded-full px-3 py-1 text-xs"
          >
            <span class="max-w-[16rem] truncate">{{ displayName(g) }}</span>
            <KunButton
              variant="light"
              color="danger"
              size="xs"
              is-icon-only
              aria-label="移除"
              class-name="ml-1"
              @click="remove(g.id)"
            >
              <KunIcon name="lucide:x" class="size-3" />
            </KunButton>
          </span>
          <span
            v-if="pickedGalgames.length === 0"
            class="text-default-400 text-xs"
          >
            未添加
          </span>
        </div>

        <div class="relative">
          <KunInput
            v-model="searchQuery"
            placeholder="搜索 galgame（标题 / vndb_id / 标签 / 别名）..."
            :dark-border="false"
          />
          <div
            v-if="searching || searchResults.length > 0"
            class="bg-content1 border-default-200 absolute z-10 mt-1 max-h-72 w-full overflow-auto rounded-lg border shadow-lg"
          >
            <div
              v-if="searching"
              class="text-default-400 px-3 py-2 text-sm"
            >
              <KunIcon name="lucide:loader-circle" class="inline size-4 animate-spin" />
              搜索中...
            </div>
            <button
              v-for="g in searchResults"
              v-else
              :key="g.id"
              class="hover:bg-default-100 flex w-full items-center gap-3 px-3 py-2 text-left text-sm"
              :disabled="pickedIds.includes(g.id)"
              :class="pickedIds.includes(g.id) ? 'opacity-40' : ''"
              @click="add(g)"
            >
              <img
                v-if="g.banner"
                :src="g.banner"
                class="bg-default-100 size-8 rounded object-cover"
                alt=""
                loading="lazy"
              />
              <div
                v-else
                class="bg-default-100 flex size-8 items-center justify-center rounded"
              >
                <KunIcon name="lucide:image" class="text-default-300 size-4" />
              </div>
              <div class="min-w-0 flex-1">
                <p class="text-foreground truncate">{{ displayName(g) }}</p>
                <p class="text-default-400 font-mono text-xs">
                  {{ g.vndb_id }}
                </p>
              </div>
              <KunIcon
                v-if="pickedIds.includes(g.id)"
                name="lucide:check"
                class="text-success size-4"
              />
            </button>
          </div>
        </div>
      </div>

      <div class="flex justify-end gap-2 pt-2">
        <KunButton variant="light" @click="emit('close')">取消</KunButton>
        <KunButton color="primary" :disabled="submitting" @click="submit">
          <KunIcon
            v-if="submitting"
            name="lucide:loader-circle"
            class="mr-1 size-4 animate-spin"
          />
          {{ isEdit ? '保存' : '创建' }}
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>
