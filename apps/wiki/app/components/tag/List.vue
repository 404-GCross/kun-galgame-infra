<script setup lang="ts">
import { TAG_CATEGORY_MAP } from '~/constants/admin'
import type { GalgameTag } from '~/shared/types/galgame'

const api = useApi()

const page = useQueryState('page', 1)
const search = useQueryState('search', '')
const limit = ref(50)

const createOpen = ref(false)

// Debounced mirror of `search` so typing doesn't fire a request per
// keystroke; useAsyncData watches the debounced value.
const debouncedSearch = ref(search.value)
let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(search, (q) => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    if (page.value !== 1) page.value = 1
    debouncedSearch.value = q
  }, 300)
})

// SSR: server-fetched + payload-hydrated (no post-hydration flash);
// re-fetches client-side when page/limit/search change (watch).
const { data, status, refresh } = await useAsyncData(
  'tag-list',
  async () => {
    if (debouncedSearch.value.trim()) {
      // /tag/search returns a flat array, no pagination
      const r = await api.get<GalgameTag[]>('/tag/search', {
        q: debouncedSearch.value
      })
      const arr = r.code === 0 ? (r.data ?? []) : []
      return { items: arr, total: arr.length }
    }
    const r = await api.get<{ items: GalgameTag[]; total: number }>('/tag', {
      page: page.value,
      limit: limit.value
    })
    return r.code === 0
      ? r.data
      : { items: [] as GalgameTag[], total: 0 }
  },
  { watch: [page, limit, debouncedSearch] }
)

const items = computed(() => data.value?.items ?? [])
const total = computed(() => data.value?.total ?? 0)
const loading = computed(() => status.value === 'pending')

const onCreated = () => {
  createOpen.value = false
  refresh()
}

const inSearchMode = computed(() => !!search.value.trim())
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-foreground text-2xl font-bold">标签管理</h1>
      <div class="flex items-center gap-3">
        <span class="text-default-500 text-sm">共 {{ total }} 个</span>
        <KunButton color="primary" @click="createOpen = true">
          <Icon name="lucide:plus" class="mr-1 size-4" />
          新建标签
        </KunButton>
      </div>
    </div>

    <div class="max-w-md">
      <KunInput
        v-model="search"
        placeholder="搜索标签（支持逗号分隔多关键字）..."
        :dark-border="false"
      />
    </div>

    <KunCard class="overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[48rem] text-sm">
          <thead class="bg-content2 text-default-500 text-left">
            <tr>
              <th class="px-4 py-3 font-medium">名称</th>
              <th class="px-4 py-3 font-medium">分类</th>
              <th class="px-4 py-3 font-medium text-right">Galgame 数</th>
              <th class="px-4 py-3 font-medium">描述</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="loading && items.length === 0">
              <td colspan="4" class="text-default-400 px-4 py-10 text-center">
                <Icon name="lucide:loader-2" class="inline size-5 animate-spin" />
              </td>
            </tr>
            <tr v-else-if="items.length === 0">
              <td colspan="4" class="text-default-400 px-4 py-10 text-center">
                暂无数据
              </td>
            </tr>
            <tr
              v-for="t in items"
              v-else
              :key="t.id"
              class="border-default-200 hover:bg-default-50 border-t transition-colors"
            >
              <td class="text-foreground px-4 py-2 font-medium">
                <NuxtLink
                  :to="`/tag/${t.id}`"
                  class="hover:text-primary"
                >
                  {{ t.name }}
                </NuxtLink>
              </td>
              <td class="px-4 py-2">
                <span
                  class="rounded-full px-2 py-0.5 text-xs"
                  :class="{
                    'bg-primary-50 text-primary':
                      TAG_CATEGORY_MAP[t.category]?.color === 'primary',
                    'bg-secondary-50 text-secondary':
                      TAG_CATEGORY_MAP[t.category]?.color === 'secondary',
                    'bg-info-50 text-info':
                      TAG_CATEGORY_MAP[t.category]?.color === 'info',
                    'bg-default-100 text-default-500':
                      !TAG_CATEGORY_MAP[t.category]
                  }"
                >
                  {{ TAG_CATEGORY_MAP[t.category]?.label ?? t.category }}
                </span>
              </td>
              <td class="text-foreground px-4 py-2 text-right tabular-nums">
                {{ t.galgame_count.toLocaleString() }}
              </td>
              <td class="text-default-500 max-w-lg truncate px-4 py-2 text-xs">
                {{ t.description || '—' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </KunCard>

    <CommonPagination
      v-if="!inSearchMode"
      :page="page"
      :total="total"
      :limit="limit"
      @update:page="page = $event"
    />
    <p v-else class="text-default-400 text-right text-xs">
      搜索模式不分页（返回前若干匹配项）
    </p>

    <TagEditModal
      :open="createOpen"
      :tag="null"
      @close="createOpen = false"
      @saved="onCreated"
    />
  </div>
</template>
