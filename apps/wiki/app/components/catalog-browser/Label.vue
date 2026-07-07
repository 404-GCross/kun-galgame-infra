<script setup lang="ts">
import type { CatalogLabelWorks } from '~/shared/types/catalog'
import {
  MEDIUM_LABEL,
  WORK_STATUS_LABEL,
  CONTENT_RATING_LABEL,
  CONTENT_RATING_COLOR,
  LABEL_KIND_LABEL,
  ATTRIBUTION_KIND_LABEL
} from '~/constants/catalog'

const route = useRoute()
const catalog = useCatalog()
const id = computed(() => route.params.id as string)
const pageSize = 50
const offset = ref(0)

const { data, status } = await useAsyncData(
  () => `catalog-label-${id.value}-${offset.value}`,
  async () => {
    const r = await catalog.labelWorks(id.value, pageSize, offset.value)
    return r.code === 0 ? (r.data as CatalogLabelWorks) : null
  },
  { watch: [id, offset] }
)
const loading = computed(() => status.value === 'pending')
const head = computed(() => data.value?.label ?? null)
const hasMore = computed(() => data.value != null && offset.value + pageSize < data.value.total)
</script>

<template>
  <div class="space-y-6">
    <NuxtLink to="/catalog-browser/search">
      <KunButton variant="light" color="default" size="sm">
        <KunIcon name="lucide:arrow-left" class="size-4" />
        搜索
      </KunButton>
    </NuxtLink>

    <div v-if="loading && !data" class="text-default-400 flex items-center justify-center py-20">
      <KunIcon name="lucide:loader-circle" class="size-6 animate-spin" />
    </div>
    <div v-else-if="!data" class="text-danger flex items-center justify-center py-20">加载失败</div>

    <template v-else>
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-foreground text-2xl font-bold">{{ head?.display_name || `厂牌 #${id}` }}</h1>
        <span class="text-default-400 text-sm tabular-nums">#{{ id }}</span>
        <span v-if="head" class="text-default-500 text-sm">{{ LABEL_KIND_LABEL[head.kind] ?? head.kind }}</span>
        <span class="text-default-500 text-sm">{{ data.total.toLocaleString() }} 部作品</span>
      </div>

      <KunCard class="p-5">
        <h2 class="text-foreground mb-3 text-lg font-semibold">作品反查（经归属边）</h2>
        <div v-if="!data.items.length" class="text-default-400 text-sm">无作品</div>
        <div
          v-for="w in data.items"
          :key="w.work_id"
          class="border-default-200 flex flex-wrap items-center gap-x-3 gap-y-1 border-b py-2 text-sm last:border-0"
        >
          <span class="text-default-400 w-16 shrink-0 text-xs tabular-nums">#{{ w.work_id }}</span>
          <NuxtLink class="text-primary min-w-0 flex-1 truncate" :to="`/catalog-browser/work/${w.work_id}`">{{ w.display_name }}</NuxtLink>
          <span class="text-default-500 text-xs">{{ MEDIUM_LABEL[w.medium_id] ?? w.medium_id }}</span>
          <span :class="`text-${CONTENT_RATING_COLOR[w.content_rating]}`" class="text-xs">
            {{ CONTENT_RATING_LABEL[w.content_rating] ?? w.content_rating }}
          </span>
          <span class="text-default-400 text-xs">{{ WORK_STATUS_LABEL[w.status] ?? w.status }}</span>
          <span class="text-default-300 text-xs">归属·{{ ATTRIBUTION_KIND_LABEL[w.kind] ?? w.kind }}</span>
        </div>

        <div v-if="data.total > pageSize" class="mt-4 flex items-center justify-between">
          <KunButton variant="light" size="sm" :disabled="offset === 0" @click="offset = Math.max(0, offset - pageSize)">
            上一页
          </KunButton>
          <span class="text-default-400 text-xs tabular-nums">
            {{ offset + 1 }}–{{ Math.min(offset + pageSize, data.total) }} / {{ data.total }}
          </span>
          <KunButton variant="light" size="sm" :disabled="!hasMore" @click="offset = offset + pageSize">下一页</KunButton>
        </div>
      </KunCard>
    </template>
  </div>
</template>
