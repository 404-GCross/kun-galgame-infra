<script setup lang="ts">
import {
  GALGAME_STATUS_MAP,
  GALGAME_STATUS_TABS,
  CONTENT_LIMIT_MAP
} from '~/constants/admin'
import type { Galgame } from '~/shared/types/galgame'

interface ListResponse {
  items: Galgame[]
  total: number
}

const api = useApi()

// URL-synced state. `status` defaults to 0 (published) so /galgame reads cleanly;
// any non-default value shows up in the URL for share/reload fidelity.
const status = useQueryState('status', 0)
const page = useQueryState('page', 1)
const search = useQueryState('search', '')
const limit = ref(20)

const items = ref<Galgame[]>([])
const total = ref(0)
const loading = ref(false)

const loadList = async () => {
  loading.value = true
  try {
    const response = await api.get<ListResponse>('/admin/galgame', {
      status: status.value,
      page: page.value,
      limit: limit.value,
      search: search.value || undefined
    })
    if (response.code === 0) {
      items.value = response.data.items
      total.value = response.data.total
    }
  } finally {
    loading.value = false
  }
}

watch([status, page, limit], loadList, { immediate: true })

let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(search, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    if (page.value !== 1) {
      page.value = 1 // also triggers reload via the watcher above
    } else {
      loadList()
    }
  }, 300)
})

const displayName = (g: Galgame) =>
  g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw || '(无标题)'

const switchTab = (id: number) => {
  status.value = id
  page.value = 1
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-foreground text-2xl font-bold">Galgame 管理</h1>
      <div class="flex items-center gap-3">
        <span class="text-default-500 text-sm">共 {{ total }} 条</span>
        <NuxtLink to="/galgame-filter">
          <KunButton variant="light">
            <Icon name="lucide:filter" class="mr-1 size-4" />
            多标签筛选
          </KunButton>
        </NuxtLink>
        <NuxtLink to="/galgame/create">
          <KunButton color="primary">
            <Icon name="lucide:plus" class="mr-1 size-4" />
            新建
          </KunButton>
        </NuxtLink>
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-3">
      <div class="border-default-200 flex rounded-lg border">
        <button
          v-for="tab in GALGAME_STATUS_TABS"
          :key="tab.id"
          class="flex items-center gap-2 px-4 py-2 text-sm transition-colors first:rounded-l-lg last:rounded-r-lg"
          :class="
            status === tab.id
              ? 'bg-primary text-white'
              : 'text-default-500 hover:bg-default-100'
          "
          @click="switchTab(tab.id)"
        >
          <Icon :name="tab.icon" class="size-4" />
          {{ tab.label }}
        </button>
      </div>

      <div class="ml-auto w-64">
        <KunInput
          v-model="search"
          placeholder="搜索 vndb_id / 标题..."
          :dark-border="false"
        />
      </div>
    </div>

    <KunCard class="overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="bg-content2 text-default-500 text-left">
            <tr>
              <th class="px-4 py-3 font-medium">封面</th>
              <th class="px-4 py-3 font-medium">标题</th>
              <th class="px-4 py-3 font-medium">VNDB</th>
              <th class="px-4 py-3 font-medium">发布日期</th>
              <th class="px-4 py-3 font-medium">状态</th>
              <th class="px-4 py-3 font-medium">分级</th>
              <th class="px-4 py-3 font-medium">更新时间</th>
              <th class="px-4 py-3 font-medium"></th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-if="loading && items.length === 0"
              class="text-default-400"
            >
              <td colspan="8" class="px-4 py-10 text-center">
                <Icon
                  name="lucide:loader-2"
                  class="inline size-5 animate-spin"
                />
              </td>
            </tr>
            <tr
              v-else-if="items.length === 0"
              class="text-default-400"
            >
              <td colspan="8" class="px-4 py-10 text-center">暂无数据</td>
            </tr>
            <tr
              v-for="g in items"
              v-else
              :key="g.id"
              class="border-default-200 hover:bg-default-50 border-t transition-colors"
            >
              <td class="px-4 py-2">
                <NuxtLink :to="`/galgame/${g.id}`">
                  <img
                    v-if="g.banner"
                    :src="g.banner"
                    class="bg-default-100 size-12 rounded object-cover"
                    alt=""
                    loading="lazy"
                  />
                  <div
                    v-else
                    class="bg-default-100 flex size-12 items-center justify-center rounded"
                  >
                    <Icon
                      name="lucide:image"
                      class="text-default-300 size-5"
                    />
                  </div>
                </NuxtLink>
              </td>
              <td class="max-w-xs truncate px-4 py-2">
                <NuxtLink
                  :to="`/galgame/${g.id}`"
                  class="text-foreground hover:text-primary block truncate font-medium"
                  :title="displayName(g)"
                >
                  {{ displayName(g) }}
                </NuxtLink>
                <div
                  v-if="g.name_en_us && g.name_en_us !== displayName(g)"
                  class="text-default-400 truncate text-xs"
                >
                  {{ g.name_en_us }}
                </div>
              </td>
              <td class="px-4 py-2 font-mono text-xs">
                <a
                  :href="`https://vndb.org/${g.vndb_id}`"
                  target="_blank"
                  class="text-default-500 hover:text-primary"
                >
                  {{ g.vndb_id }}
                </a>
              </td>
              <td class="text-default-500 px-4 py-2">{{ g.released }}</td>
              <td class="px-4 py-2">
                <span
                  class="rounded-full px-2 py-0.5 text-xs"
                  :class="{
                    'bg-success-50 text-success':
                      GALGAME_STATUS_MAP[g.status]?.color === 'success',
                    'bg-warning-50 text-warning':
                      GALGAME_STATUS_MAP[g.status]?.color === 'warning',
                    'bg-danger-50 text-danger':
                      GALGAME_STATUS_MAP[g.status]?.color === 'danger'
                  }"
                >
                  {{ GALGAME_STATUS_MAP[g.status]?.label ?? '未知' }}
                </span>
              </td>
              <td class="px-4 py-2">
                <span
                  class="rounded px-2 py-0.5 text-xs"
                  :class="
                    CONTENT_LIMIT_MAP[g.content_limit]?.color === 'danger'
                      ? 'bg-danger-50 text-danger'
                      : 'bg-default-100 text-default-500'
                  "
                >
                  {{ CONTENT_LIMIT_MAP[g.content_limit]?.label ?? g.content_limit }}
                </span>
              </td>
              <td class="text-default-400 px-4 py-2 text-xs">
                {{ g.updated?.slice(0, 10) }}
              </td>
              <td class="px-4 py-2">
                <NuxtLink
                  :to="`/galgame/${g.id}`"
                  class="text-primary hover:underline"
                >
                  查看
                </NuxtLink>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </KunCard>

    <CommonPagination
      :page="page"
      :total="total"
      :limit="limit"
      @update:page="page = $event"
    />
  </div>
</template>
