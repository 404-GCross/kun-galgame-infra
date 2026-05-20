<script setup lang="ts">
import { GALGAME_STATUS_MAP } from '~/constants/admin'
import type { Galgame, GalgameEngine } from '~/shared/types/galgame'
import { formatReleaseDate } from '~/shared/utils/format'

const api = useApi()
const route = useRoute()
const router = useRouter()

const id = computed(() => Number(route.params.id))
const editOpen = ref(false)

const page = useQueryState('page', 1)
const limit = ref(24)

// SSR: server-fetched, payload-hydrated (no post-hydration flash);
// re-fetches on id/page change (watch) or after edit (refresh()).
const { data, status, refresh } = await useAsyncData(
  'engine-detail',
  async () => {
    const r = await api.get<{
      engine: GalgameEngine & { alias?: string[] | unknown }
      galgames: Galgame[]
      total: number
    }>('/engine/_', {
      engine_id: id.value,
      page: page.value,
      limit: limit.value
    })
    return r.code === 0 ? r.data : null
  },
  { watch: [id, page, limit] }
)

const engine = computed(() => data.value?.engine ?? null)
const galgames = computed(() => data.value?.galgames ?? [])
const total = computed(() => data.value?.total ?? 0)
const loading = computed(() => status.value === 'pending')

const aliasList = computed<string[]>(() => {
  const raw = engine.value?.alias
  if (Array.isArray(raw)) return raw as string[]
  return []
})

const displayName = (g: Galgame) =>
  g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw || '(无标题)'

const onSaved = () => {
  editOpen.value = false
  refresh()
}

// Two-step delete: safe-by-default, ?force=true purges references. See
// tag/Detail.vue for rationale.
const handleDelete = async () => {
  if (!engine.value) return
  if (
    !(await useKunConfirm({
      title: '删除引擎',
      content: `确定删除引擎「${engine.value.name}」吗？`,
      confirmText: '删除',
      danger: true
    }))
  )
    return
  let r = await api.delete(`/engine/${id.value}`)
  if (r.code !== 0 && r.message.includes('force=true')) {
    if (
      !(await useKunConfirm({
        title: '强制删除',
        content: `${r.message}\n\n仍要强制清除全部引用并硬删除吗？此操作不可恢复。`,
        confirmText: '强制删除',
        danger: true
      }))
    )
      return
    r = await api.delete(`/engine/${id.value}?force=true`)
  }
  if (r.code === 0) {
    useKunMessage('删除成功', 'success')
    router.push('/engine')
  } else {
    useKunMessage(r.message || '删除失败', 'error')
  }
}
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
      <h1 class="text-foreground text-2xl font-bold">引擎详情</h1>
    </div>

    <div
      v-if="loading && !engine"
      class="text-default-400 flex items-center justify-center py-20"
    >
      <Icon name="lucide:loader-2" class="size-6 animate-spin" />
    </div>

    <template v-else-if="engine">
      <KunCard class="p-6">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0 space-y-2">
            <h2 class="text-foreground text-xl font-bold">{{ engine.name }}</h2>
            <p class="text-default-500 text-sm">
              已发布 galgame: {{ engine.galgame_count }}
            </p>
            <p v-if="engine.description" class="text-default-500 text-sm">
              {{ engine.description }}
            </p>
            <div v-if="aliasList.length > 0" class="flex flex-wrap gap-1 pt-2">
              <span
                v-for="a in aliasList"
                :key="a"
                class="bg-default-100 text-default-600 rounded px-2 py-0.5 text-xs"
              >
                {{ a }}
              </span>
            </div>
          </div>
          <div class="flex shrink-0 gap-2">
            <KunButton variant="light" @click="editOpen = true">
              <Icon name="lucide:pencil" class="mr-1 size-4" />
              编辑
            </KunButton>
            <KunButton color="danger" variant="light" @click="handleDelete">
              <Icon name="lucide:trash-2" class="mr-1 size-4" />
              删除
            </KunButton>
          </div>
        </div>
      </KunCard>

      <KunCard class="overflow-hidden">
        <div class="border-default-200 border-b px-4 py-3">
          <h3 class="text-foreground text-base font-semibold">
            关联 galgame ({{ total }})
          </h3>
        </div>

        <div
          v-if="galgames.length === 0"
          class="text-default-400 px-4 py-10 text-center"
        >
          暂无关联
        </div>

        <div
          v-else
          class="grid grid-cols-2 gap-3 p-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6"
        >
          <NuxtLink
            v-for="g in galgames"
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
              {{ GALGAME_STATUS_MAP[g.status]?.label ?? '?' }} · {{ formatReleaseDate(g.release_date, g.release_date_tba) }}
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

      <EngineEditModal
        :open="editOpen"
        :engine="engine"
        :aliases="aliasList"
        @close="editOpen = false"
        @saved="onSaved"
      />
    </template>
  </div>
</template>
