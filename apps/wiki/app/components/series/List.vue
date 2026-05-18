<script setup lang="ts">
import type { GalgameSeries } from '~/shared/types/galgame'

const api = useApi()
const router = useRouter()

const page = useQueryState('page', 1)
const limit = ref(24)

const createOpen = ref(false)

// SSR: server-fetched + payload-hydrated; re-fetches on page/limit change.
const { data, status, refresh } = await useAsyncData(
  'series-list',
  async () => {
    const r = await api.get<{ items: GalgameSeries[]; total: number }>(
      '/series',
      { page: page.value, limit: limit.value }
    )
    return r.code === 0
      ? r.data
      : { items: [] as GalgameSeries[], total: 0 }
  },
  { watch: [page, limit] }
)

const items = computed(() => data.value?.items ?? [])
const total = computed(() => data.value?.total ?? 0)
const loading = computed(() => status.value === 'pending')

const onCreated = (id?: number) => {
  createOpen.value = false
  if (id) router.push(`/series/${id}`)
  else refresh()
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-foreground text-2xl font-bold">系列管理</h1>
      <div class="flex items-center gap-3">
        <span class="text-default-500 text-sm">共 {{ total }} 个</span>
        <KunButton color="primary" @click="createOpen = true">
          <Icon name="lucide:plus" class="mr-1 size-4" />
          新建系列
        </KunButton>
      </div>
    </div>

    <KunCard class="overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="bg-content2 text-default-500 text-left">
            <tr>
              <th class="px-4 py-3 font-medium">名称</th>
              <th class="px-4 py-3 font-medium">描述</th>
              <th class="px-4 py-3 font-medium text-right">Galgame 数</th>
              <th class="px-4 py-3 font-medium">更新时间</th>
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
              v-for="s in items"
              v-else
              :key="s.id"
              class="border-default-200 hover:bg-default-50 border-t transition-colors"
            >
              <td class="text-foreground px-4 py-2 font-medium">
                <NuxtLink :to="`/series/${s.id}`" class="hover:text-primary">
                  {{ s.name }}
                </NuxtLink>
              </td>
              <td class="text-default-500 max-w-lg truncate px-4 py-2 text-xs">
                {{ s.description || '—' }}
              </td>
              <td class="text-foreground px-4 py-2 text-right tabular-nums">
                {{ s.galgame_count.toLocaleString() }}
              </td>
              <td class="text-default-400 px-4 py-2 text-xs">
                {{ (s as unknown as { updated?: string }).updated?.slice(0, 10) || '—' }}
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

    <SeriesEditModal
      :open="createOpen"
      :series="null"
      @close="createOpen = false"
      @saved="onCreated"
    />
  </div>
</template>
