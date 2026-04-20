<script setup lang="ts">
import type { GalgameSeries } from '~/shared/types/galgame'

const api = useApi()

const page = useQueryState('page', 1)
const limit = ref(24)

const items = ref<GalgameSeries[]>([])
const total = ref(0)
const loading = ref(false)

const loadList = async () => {
  loading.value = true
  try {
    const response = await api.get<{ items: GalgameSeries[]; total: number }>(
      '/series',
      { page: page.value, limit: limit.value }
    )
    if (response.code === 0) {
      items.value = response.data.items
      total.value = response.data.total
    }
  } finally {
    loading.value = false
  }
}

watch([page, limit], loadList, { immediate: true })
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-foreground text-2xl font-bold">系列管理</h1>
      <span class="text-default-500 text-sm">共 {{ total }} 个</span>
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
              <td class="text-foreground px-4 py-2 font-medium">{{ s.name }}</td>
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
  </div>
</template>
