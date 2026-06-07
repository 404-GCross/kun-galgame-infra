<script setup lang="ts">
import type { GalgameEngine } from '~/shared/types/galgame'

const api = useApi()

// /engine returns a flat array — small dataset, no server-side pagination.
// Client-side filter via URL-synced `search`.
const search = useQueryState('search', '')
const createOpen = ref(false)

// SSR: the full engine list (small dataset) is server-fetched and
// payload-hydrated; filtering is client-side over it.
const { data, status, refresh } = await useAsyncData('engine-list', async () => {
  const r = await api.get<GalgameEngine[]>('/engine')
  return r.code === 0 ? (r.data ?? []) : []
})

const items = computed(() => data.value ?? [])
const loading = computed(() => status.value === 'pending')

const onCreated = () => {
  createOpen.value = false
  refresh()
}

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return items.value
  return items.value.filter((e) => e.name.toLowerCase().includes(q))
})
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-foreground text-2xl font-bold">引擎管理</h1>
      <div class="flex items-center gap-3">
        <span class="text-default-500 text-sm">
          共 {{ items.length }} 个{{ search ? ` · 匹配 ${filtered.length}` : '' }}
        </span>
        <KunButton color="primary" @click="createOpen = true">
          <KunIcon name="lucide:plus" class="mr-1 size-4" />
          新建引擎
        </KunButton>
      </div>
    </div>

    <div class="max-w-md">
      <KunInput
        v-model="search"
        placeholder="过滤引擎名..."
        :dark-border="false"
      />
    </div>

    <KunCard class="overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[40rem] text-sm">
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
                <KunIcon name="lucide:loader-circle" class="inline size-5 animate-spin" />
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
              <td class="text-foreground px-4 py-2 font-medium">
                <NuxtLink :to="`/engine/${e.id}`" class="hover:text-primary">
                  {{ e.name }}
                </NuxtLink>
              </td>
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

    <EngineEditModal
      :open="createOpen"
      :engine="null"
      @close="createOpen = false"
      @saved="onCreated"
    />
  </div>
</template>
