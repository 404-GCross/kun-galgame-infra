<script setup lang="ts">
import { TAG_CATEGORY_MAP, GALGAME_STATUS_MAP } from '~/constants/admin'
import type { Galgame, GalgameTag } from '~/shared/types/galgame'

const api = useApi()
const route = useRoute()
const router = useRouter()

const id = computed(() => Number(route.params.id))

const tag = ref<GalgameTag | null>(null)
const aliases = ref<string[]>([])
const galgames = ref<Galgame[]>([])
const total = ref(0)
const loading = ref(false)
const editOpen = ref(false)

const page = useQueryState('page', 1)
const limit = ref(24)

const load = async () => {
  loading.value = true
  try {
    const response = await api.get<{
      tag: GalgameTag & { alias?: { name: string }[] }
      galgames: Galgame[]
      total: number
    }>(`/tag/_`, {
      tag_id: id.value,
      page: page.value,
      limit: limit.value
    })
    if (response.code === 0) {
      tag.value = response.data.tag
      aliases.value = (response.data.tag.alias ?? []).map((a) => a.name)
      galgames.value = response.data.galgames
      total.value = response.data.total
    }
  } finally {
    loading.value = false
  }
}

watch([id, page, limit], load, { immediate: true })

const displayName = (g: Galgame) =>
  g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw || '(无标题)'

const onSaved = () => {
  editOpen.value = false
  load()
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
      <h1 class="text-foreground text-2xl font-bold">标签详情</h1>
    </div>

    <div
      v-if="loading && !tag"
      class="text-default-400 flex items-center justify-center py-20"
    >
      <Icon name="lucide:loader-2" class="size-6 animate-spin" />
    </div>

    <template v-else-if="tag">
      <KunCard class="p-6">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0 space-y-2">
            <h2 class="text-foreground text-xl font-bold">{{ tag.name }}</h2>
            <div class="flex flex-wrap items-center gap-2 text-sm">
              <span
                class="rounded-full px-2 py-0.5 text-xs"
                :class="{
                  'bg-primary-50 text-primary':
                    TAG_CATEGORY_MAP[tag.category]?.color === 'primary',
                  'bg-secondary-50 text-secondary':
                    TAG_CATEGORY_MAP[tag.category]?.color === 'secondary',
                  'bg-info-50 text-info':
                    TAG_CATEGORY_MAP[tag.category]?.color === 'info'
                }"
              >
                {{ TAG_CATEGORY_MAP[tag.category]?.label ?? tag.category }}
              </span>
              <span class="text-default-500">
                已发布 galgame: {{ tag.galgame_count }}
              </span>
            </div>
            <p v-if="tag.description" class="text-default-500 text-sm">
              {{ tag.description }}
            </p>
            <div v-if="aliases.length > 0" class="flex flex-wrap gap-1 pt-2">
              <span
                v-for="a in aliases"
                :key="a"
                class="bg-default-100 text-default-600 rounded px-2 py-0.5 text-xs"
              >
                {{ a }}
              </span>
            </div>
          </div>
          <KunButton variant="light" @click="editOpen = true">
            <Icon name="lucide:pencil" class="mr-1 size-4" />
            编辑
          </KunButton>
        </div>
      </KunCard>

      <KunCard class="overflow-hidden">
        <div class="border-default-200 border-b px-4 py-3">
          <h3 class="text-foreground text-base font-semibold">
            关联 galgame ({{ total }})
          </h3>
        </div>

        <div v-if="galgames.length === 0" class="text-default-400 px-4 py-10 text-center">
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
              <div
                v-else
                class="flex size-full items-center justify-center"
              >
                <Icon
                  name="lucide:image"
                  class="text-default-300 size-8"
                />
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

      <CommonPagination
        :page="page"
        :total="total"
        :limit="limit"
        @update:page="page = $event"
      />

      <TagEditModal
        :open="editOpen"
        :tag="tag"
        :aliases="aliases"
        @close="editOpen = false"
        @saved="onSaved"
      />
    </template>
  </div>
</template>
