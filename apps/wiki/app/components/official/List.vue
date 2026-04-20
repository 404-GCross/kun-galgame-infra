<script setup lang="ts">
import { OFFICIAL_CATEGORY_MAP } from '~/constants/admin'
import type { GalgameOfficial } from '~/shared/types/galgame'

const api = useApi()

const page = useQueryState('page', 1)
const search = useQueryState('search', '')
const limit = ref(50)

const items = ref<GalgameOfficial[]>([])
const total = ref(0)
const loading = ref(false)

const loadList = async () => {
  loading.value = true
  try {
    if (search.value.trim()) {
      const response = await api.get<GalgameOfficial[]>('/official/search', {
        q: search.value
      })
      if (response.code === 0) {
        items.value = response.data ?? []
        total.value = items.value.length
      }
    } else {
      const response = await api.get<{
        items: GalgameOfficial[]
        total: number
      }>('/official', { page: page.value, limit: limit.value })
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
      <h1 class="text-foreground text-2xl font-bold">会社管理</h1>
      <span class="text-default-500 text-sm">共 {{ total }} 家</span>
    </div>

    <div class="relative max-w-md">
      <Icon
        name="lucide:search"
        class="text-default-400 absolute top-1/2 left-3 size-4 -translate-y-1/2"
      />
      <input
        v-model="search"
        type="text"
        placeholder="搜索会社（名称 / 原名 / 别名，逗号分隔多关键字）..."
        class="bg-content1 border-default-200 placeholder:text-default-400 focus:border-primary w-full rounded-lg border py-2 pr-3 pl-9 text-sm outline-none"
      />
    </div>

    <KunCard class="overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="bg-content2 text-default-500 text-left">
            <tr>
              <th class="px-4 py-3 font-medium">名称</th>
              <th class="px-4 py-3 font-medium">原名</th>
              <th class="px-4 py-3 font-medium">类型</th>
              <th class="px-4 py-3 font-medium">语言</th>
              <th class="px-4 py-3 font-medium text-right">Galgame 数</th>
              <th class="px-4 py-3 font-medium">链接</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="loading && items.length === 0">
              <td colspan="6" class="text-default-400 px-4 py-10 text-center">
                <Icon name="lucide:loader-2" class="inline size-5 animate-spin" />
              </td>
            </tr>
            <tr v-else-if="items.length === 0">
              <td colspan="6" class="text-default-400 px-4 py-10 text-center">
                暂无数据
              </td>
            </tr>
            <tr
              v-for="o in items"
              v-else
              :key="o.id"
              class="border-default-200 hover:bg-default-50 border-t transition-colors"
            >
              <td class="text-foreground px-4 py-2 font-medium">{{ o.name }}</td>
              <td class="text-default-500 px-4 py-2">
                {{ o.original && o.original !== o.name ? o.original : '—' }}
              </td>
              <td class="px-4 py-2">
                <span
                  class="rounded px-2 py-0.5 text-xs"
                  :class="{
                    'bg-primary-50 text-primary':
                      OFFICIAL_CATEGORY_MAP[o.category]?.color === 'primary',
                    'bg-info-50 text-info':
                      OFFICIAL_CATEGORY_MAP[o.category]?.color === 'info',
                    'bg-default-100 text-default-500':
                      OFFICIAL_CATEGORY_MAP[o.category]?.color === 'default' ||
                      !OFFICIAL_CATEGORY_MAP[o.category]
                  }"
                >
                  {{ OFFICIAL_CATEGORY_MAP[o.category]?.label ?? o.category }}
                </span>
              </td>
              <td class="text-default-500 px-4 py-2 text-xs uppercase">
                {{ o.lang || '—' }}
              </td>
              <td class="text-foreground px-4 py-2 text-right tabular-nums">
                {{ o.galgame_count.toLocaleString() }}
              </td>
              <td class="px-4 py-2 text-xs">
                <a
                  v-if="o.link"
                  :href="o.link"
                  target="_blank"
                  class="text-primary hover:underline"
                >
                  访问 ↗
                </a>
                <span v-else class="text-default-400">—</span>
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
