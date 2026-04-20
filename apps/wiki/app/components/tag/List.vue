<script setup lang="ts">
import { TAG_CATEGORY_MAP } from '~/constants/admin'
import type { GalgameTag } from '~/shared/types/galgame'

const api = useApi()

const page = useQueryState('page', 1)
const search = useQueryState('search', '')
const limit = ref(50)

const items = ref<GalgameTag[]>([])
const total = ref(0)
const loading = ref(false)

const loadList = async () => {
  loading.value = true
  try {
    if (search.value.trim()) {
      // /tag/search returns a flat array, no pagination
      const response = await api.get<GalgameTag[]>('/tag/search', {
        q: search.value
      })
      if (response.code === 0) {
        items.value = response.data ?? []
        total.value = items.value.length
      }
    } else {
      const response = await api.get<{ items: GalgameTag[]; total: number }>(
        '/tag',
        { page: page.value, limit: limit.value }
      )
      if (response.code === 0) {
        items.value = response.data.items
        total.value = response.data.total
      }
    }
  } finally {
    loading.value = false
  }
}

watch([page, limit], loadList, { immediate: true })

let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(search, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    if (page.value !== 1) {
      page.value = 1
    } else {
      loadList()
    }
  }, 300)
})

const inSearchMode = computed(() => !!search.value.trim())
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-foreground text-2xl font-bold">标签管理</h1>
      <span class="text-default-500 text-sm">共 {{ total }} 个</span>
    </div>

    <div class="relative max-w-md">
      <Icon
        name="lucide:search"
        class="text-default-400 absolute top-1/2 left-3 size-4 -translate-y-1/2"
      />
      <input
        v-model="search"
        type="text"
        placeholder="搜索标签（支持逗号分隔多关键字）..."
        class="bg-content1 border-default-200 placeholder:text-default-400 focus:border-primary w-full rounded-lg border py-2 pr-3 pl-9 text-sm outline-none"
      />
    </div>

    <KunCard class="overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
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
                {{ t.name }}
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
  </div>
</template>
