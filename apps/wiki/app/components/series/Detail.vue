<script setup lang="ts">
import { GALGAME_STATUS_MAP } from '~/constants/admin'
import type { Galgame, GalgameSeries } from '~/shared/types/galgame'

const api = useApi()
const route = useRoute()
const router = useRouter()

const id = computed(() => Number(route.params.id))

const editOpen = ref(false)

// SSR: fetched on the server, serialized into the payload, reused on
// hydration (no post-hydration flash). Re-fetches client-side when the
// route id changes (watch) or after edit (refresh()).
// /series/:id returns the series with preloaded Galgame[] (status=0 only)
const { data: series, status, refresh } = await useAsyncData(
  'series-detail',
  async () => {
    const r = await api.get<GalgameSeries & { galgame?: Galgame[] }>(
      `/series/${id.value}`
    )
    return r.code === 0 ? r.data : null
  },
  { watch: [id] }
)
const loading = computed(() => status.value === 'pending')

const displayName = (g: Galgame) =>
  g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw || '(无标题)'

const onSaved = () => {
  editOpen.value = false
  refresh()
}

const handleDelete = async () => {
  if (!series.value) return
  if (
    !(await useKunConfirm({
      title: '删除系列',
      content: `确定删除系列「${series.value.name}」吗？galgame 不会被删除，但会脱离此系列。`,
      confirmText: '删除',
      danger: true
    }))
  )
    return
  const response = await api.delete(`/series/${id.value}`)
  if (response.code === 0) {
    useKunMessage('删除成功', 'success')
    router.push('/series')
  } else {
    useKunMessage(response.message || '删除失败', 'error')
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
      <h1 class="text-foreground text-2xl font-bold">系列详情</h1>
    </div>

    <div
      v-if="loading && !series"
      class="text-default-400 flex items-center justify-center py-20"
    >
      <Icon name="lucide:loader-2" class="size-6 animate-spin" />
    </div>

    <template v-else-if="series">
      <KunCard class="p-6">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0 space-y-2">
            <h2 class="text-foreground text-xl font-bold">{{ series.name }}</h2>
            <p v-if="series.description" class="text-default-500 text-sm">
              {{ series.description }}
            </p>
            <p class="text-default-500 text-sm">
              作品数: {{ series.galgame?.length ?? 0 }}
            </p>
          </div>
          <div class="flex gap-2">
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
            系列作品 ({{ series.galgame?.length ?? 0 }})
          </h3>
        </div>

        <div
          v-if="!series.galgame || series.galgame.length === 0"
          class="text-default-400 px-4 py-10 text-center"
        >
          暂无作品
        </div>

        <div
          v-else
          class="grid grid-cols-2 gap-3 p-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6"
        >
          <NuxtLink
            v-for="g in series.galgame"
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
              {{ GALGAME_STATUS_MAP[g.status]?.label ?? '?' }} · {{ g.released }}
            </p>
          </NuxtLink>
        </div>
      </KunCard>

      <SeriesEditModal
        :open="editOpen"
        :series="series"
        @close="editOpen = false"
        @saved="onSaved"
      />
    </template>
  </div>
</template>
