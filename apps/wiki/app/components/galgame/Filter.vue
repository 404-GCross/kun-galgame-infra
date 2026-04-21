<script setup lang="ts">
import { GALGAME_STATUS_MAP, TAG_CATEGORY_MAP } from '~/constants/admin'
import type { Galgame, GalgameTag } from '~/shared/types/galgame'

const api = useApi()
const router = useRouter()
const route = useRoute()

// URL-synced tag IDs (comma-separated). Also persist content_limit + page.
const tagIdsParam = useQueryState('tag_ids', '')
const contentLimit = useQueryState('content_limit', '')
const page = useQueryState('page', 1)
const limit = ref(24)

// Parse tag_ids from URL; also hydrate tag names via GET /tag/multi initial fetch
const selectedTags = ref<GalgameTag[]>([])
const selectedIds = computed(() =>
  tagIdsParam.value
    ? tagIdsParam.value
        .split(',')
        .map((s) => Number(s.trim()))
        .filter((n) => Number.isFinite(n) && n > 0)
    : []
)

// Tag search state
const tagQuery = ref('')
const tagSuggestions = ref<GalgameTag[]>([])
const searchingTags = ref(false)

let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(tagQuery, (q) => {
  if (searchTimer) clearTimeout(searchTimer)
  if (!q.trim()) {
    tagSuggestions.value = []
    return
  }
  searchTimer = setTimeout(async () => {
    searchingTags.value = true
    try {
      const response = await api.get<GalgameTag[]>('/tag/search', { q })
      if (response.code === 0) tagSuggestions.value = response.data ?? []
    } finally {
      searchingTags.value = false
    }
  }, 250)
})

const addTag = (t: GalgameTag) => {
  if (selectedIds.value.includes(t.id)) return
  selectedTags.value = [...selectedTags.value, t]
  tagIdsParam.value = [...selectedIds.value, t.id].join(',')
  tagQuery.value = ''
  tagSuggestions.value = []
  page.value = 1
}

const removeTag = (id: number) => {
  selectedTags.value = selectedTags.value.filter((t) => t.id !== id)
  tagIdsParam.value = selectedIds.value.filter((x) => x !== id).join(',')
  page.value = 1
}

// Results
const items = ref<Galgame[]>([])
const total = ref(0)
const loading = ref(false)

const loadResults = async () => {
  if (selectedIds.value.length === 0) {
    items.value = []
    total.value = 0
    return
  }
  loading.value = true
  try {
    const query: Record<string, string | number | undefined> = {
      page: page.value,
      limit: limit.value
    }
    // tag_ids=1&tag_ids=2 — backend accepts repeated query params
    selectedIds.value.forEach((id, i) => {
      query[`tag_ids[${i}]`] = id
    })
    if (contentLimit.value) query.content_limit = contentLimit.value

    const response = await api.get<{ items: Galgame[]; total: number }>(
      '/tag/multi',
      query
    )
    if (response.code === 0) {
      items.value = response.data.items
      total.value = response.data.total
    }
  } finally {
    loading.value = false
  }
}

watch([tagIdsParam, contentLimit, page], loadResults, { immediate: true })

// On first load with tag_ids in URL but no tag names, hydrate by fetching each tag
onMounted(async () => {
  if (selectedIds.value.length > 0 && selectedTags.value.length === 0) {
    const results = await Promise.all(
      selectedIds.value.map((id) =>
        api
          .get<{ tag: GalgameTag; galgames: Galgame[]; total: number }>(
            '/tag/_',
            { tag_id: id, page: 1, limit: 1 }
          )
          .then((r) => (r.code === 0 ? r.data.tag : null))
          .catch(() => null)
      )
    )
    selectedTags.value = results.filter((t): t is GalgameTag => !!t)
  }
})

const displayName = (g: Galgame) =>
  g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw || '(无标题)'
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center gap-3">
      <button
        class="text-default-500 hover:bg-default-100 rounded-lg p-2 transition-colors"
        @click="router.back()"
      >
        <Icon name="lucide:arrow-left" class="size-5" />
      </button>
      <h1 class="text-foreground text-2xl font-bold">多标签交集筛选</h1>
    </div>

    <KunCard class="space-y-3 p-6">
      <p class="text-default-500 text-sm">
        选择多个标签，返回同时命中所有标签的 galgame
      </p>

      <div class="flex flex-wrap items-center gap-2">
        <span
          v-for="t in selectedTags"
          :key="t.id"
          class="bg-primary-50 text-primary flex items-center gap-1 rounded-full px-3 py-1 text-sm"
        >
          {{ t.name }}
          <span
            v-if="TAG_CATEGORY_MAP[t.category]"
            class="text-default-400 text-xs"
          >
            ({{ TAG_CATEGORY_MAP[t.category].label }})
          </span>
          <button
            class="hover:text-danger ml-1"
            title="移除"
            @click="removeTag(t.id)"
          >
            <Icon name="lucide:x" class="size-3" />
          </button>
        </span>
        <span
          v-if="selectedTags.length === 0"
          class="text-default-400 text-sm"
        >
          尚未选择标签
        </span>
      </div>

      <div class="relative max-w-md">
        <KunInput
          v-model="tagQuery"
          placeholder="搜索添加标签..."
          :dark-border="false"
        />
        <div
          v-if="tagSuggestions.length > 0"
          class="bg-content1 border-default-200 absolute z-10 mt-1 max-h-64 w-full overflow-auto rounded-lg border shadow-lg"
        >
          <button
            v-for="t in tagSuggestions"
            :key="t.id"
            class="hover:bg-default-100 flex w-full items-center justify-between px-3 py-2 text-left text-sm"
            @click="addTag(t)"
          >
            <span class="text-foreground">{{ t.name }}</span>
            <span class="text-default-400 text-xs">
              {{ TAG_CATEGORY_MAP[t.category]?.label ?? t.category }} ·
              {{ t.galgame_count }}
            </span>
          </button>
        </div>
      </div>

      <div class="flex items-center gap-3">
        <span class="text-default-500 text-sm">内容分级：</span>
        <div class="flex gap-2">
          <KunButton
            :variant="contentLimit === '' ? 'solid' : 'light'"
            size="sm"
            @click="
              () => {
                contentLimit = ''
                page = 1
              }
            "
          >
            全部
          </KunButton>
          <KunButton
            :variant="contentLimit === 'sfw' ? 'solid' : 'light'"
            size="sm"
            @click="
              () => {
                contentLimit = 'sfw'
                page = 1
              }
            "
          >
            SFW
          </KunButton>
          <KunButton
            :variant="contentLimit === 'nsfw' ? 'solid' : 'light'"
            color="danger"
            size="sm"
            @click="
              () => {
                contentLimit = 'nsfw'
                page = 1
              }
            "
          >
            NSFW
          </KunButton>
        </div>
      </div>
    </KunCard>

    <div v-if="selectedIds.length === 0" class="text-default-400 py-10 text-center">
      先选一个或多个标签以查看交集结果
    </div>

    <template v-else>
      <div class="flex items-center justify-between">
        <h2 class="text-foreground text-lg font-semibold">
          匹配 {{ total }} 个 galgame
        </h2>
      </div>

      <KunCard class="overflow-hidden">
        <div
          v-if="loading && items.length === 0"
          class="text-default-400 py-10 text-center"
        >
          <Icon name="lucide:loader-2" class="inline size-5 animate-spin" />
        </div>
        <div v-else-if="items.length === 0" class="text-default-400 py-10 text-center">
          无匹配结果
        </div>
        <div
          v-else
          class="grid grid-cols-2 gap-3 p-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6"
        >
          <NuxtLink
            v-for="g in items"
            :key="g.id"
            :to="`/galgame/${g.id}`"
            class="group block"
          >
            <div class="bg-default-100 aspect-[3/4] overflow-hidden rounded">
              <img
                v-if="g.banner"
                :src="g.banner"
                class="size-full object-cover transition-transform group-hover:scale-105"
                alt=""
                loading="lazy"
              />
              <div v-else class="flex size-full items-center justify-center">
                <Icon name="lucide:image" class="text-default-300 size-8" />
              </div>
            </div>
            <p
              class="text-foreground group-hover:text-primary mt-2 line-clamp-2 text-xs"
              :title="displayName(g)"
            >
              {{ displayName(g) }}
            </p>
            <p class="text-default-400 mt-0.5 text-[10px]">
              {{ GALGAME_STATUS_MAP[g.status]?.label ?? '?' }}
            </p>
          </NuxtLink>
        </div>
      </KunCard>

      <CommonPagination
        :page="page"
        :total="total"
        :limit="limit"
        @update:page="page = $event"
      />
    </template>
  </div>
</template>
