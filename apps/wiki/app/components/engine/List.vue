<script setup lang="ts">
import type { GalgameEngine } from '~/shared/types/galgame'

const api = useApi()

// /engine returns a flat array — small dataset, no server-side pagination.
// Client-side filter via URL-synced `search`.
const search = useQueryState('search', '')
const items = ref<GalgameEngine[]>([])
const loading = ref(false)

const load = async () => {
  loading.value = true
  try {
    const response = await api.get<GalgameEngine[]>('/engine')
    if (response.code === 0) {
      items.value = response.data ?? []
    }
  } finally {
    loading.value = false
  }
}

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return items.value
  return items.value.filter((e) => e.name.toLowerCase().includes(q))
})

onMounted(load)
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-foreground text-2xl font-bold">引擎管理</h1>
      <span class="text-default-500 text-sm">
        共 {{ items.length }} 个{{ search ? ` · 匹配 ${filtered.length}` : '' }}
      </span>
    </div>

    <div class="relative max-w-md">
      <Icon
        name="lucide:search"
        class="text-default-400 absolute top-1/2 left-3 size-4 -translate-y-1/2"
      />
      <input
        v-model="search"
        type="text"
        placeholder="过滤引擎名..."
        class="bg-content1 border-default-200 placeholder:text-default-400 focus:border-primary w-full rounded-lg border py-2 pr-3 pl-9 text-sm outline-none"
      />
    </div>

    <KunCard class="overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="bg-content2 text-default-500 text-left">
            <tr>
              <th class="px-4 py-3 font-medium">名称</th>
              <th class="px-4 py-3 font-medium text-right">Galgame 数</th>
              <th class="px-4 py-3 font-medium">描述</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="loading && items.length === 0">
              <td colspan="3" class="text-default-400 px-4 py-10 text-center">
                <Icon name="lucide:loader-2" class="inline size-5 animate-spin" />
              </td>
            </tr>
            <tr v-else-if="filtered.length === 0">
              <td colspan="3" class="text-default-400 px-4 py-10 text-center">
                {{ search ? '无匹配结果' : '暂无数据' }}
              </td>
            </tr>
            <tr
              v-for="e in filtered"
              v-else
              :key="e.id"
              class="border-default-200 hover:bg-default-50 border-t transition-colors"
            >
              <td class="text-foreground px-4 py-2 font-medium">{{ e.name }}</td>
              <td class="text-foreground px-4 py-2 text-right tabular-nums">
                {{ e.galgame_count.toLocaleString() }}
              </td>
              <td class="text-default-500 max-w-lg truncate px-4 py-2 text-xs">
                {{ e.description || '—' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </KunCard>
  </div>
</template>
