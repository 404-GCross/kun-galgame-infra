<script setup lang="ts">
import type { CatalogWorkDetail, CatalogCredits, CatalogAnchorRef } from '~/shared/types/catalog'
import {
  MEDIUM_LABEL,
  WORK_STATUS_LABEL,
  CONTENT_RATING_LABEL,
  CONTENT_RATING_COLOR,
  LINK_KIND_LABEL,
  LINK_KIND_COLOR,
  ATTRIBUTION_KIND_LABEL,
  LABEL_KIND_LABEL,
  TITLE_KIND_LABEL
} from '~/constants/catalog'

const route = useRoute()
const catalog = useCatalog()
const id = computed(() => route.params.id as string)

const { data, status } = await useAsyncData(
  () => `catalog-work-${id.value}`,
  async () => {
    const [w, c] = await Promise.all([catalog.work(id.value), catalog.credits(id.value)])
    return {
      work: w.code === 0 ? (w.data as CatalogWorkDetail) : null,
      credits: c.code === 0 ? (c.data as CatalogCredits) : null
    }
  },
  { watch: [id] }
)
const loading = computed(() => status.value === 'pending')
const work = computed(() => data.value?.work ?? null)
const credits = computed(() => data.value?.credits ?? null)

// Flatten every release's anchors into one provenance list (the machine view).
const provenance = computed(() => {
  const w = work.value
  if (!w) return []
  const rows: (CatalogAnchorRef & { release: number })[] = []
  for (const rel of w.releases) {
    for (const a of rel.anchors) rows.push({ ...a, release: rel.id })
  }
  return rows.sort((a, b) => a.link_kind - b.link_kind)
})

const fuzzyDate = (r: { released_y?: number; released_m?: number; released_d?: number }) =>
  [r.released_y, r.released_m, r.released_d].filter(Boolean).join('-') || '—'
</script>

<template>
  <div class="space-y-6">
    <NuxtLink to="/catalog-browser">
      <KunButton variant="light" color="default" size="sm">
        <KunIcon name="lucide:arrow-left" class="size-4" />
        返回
      </KunButton>
    </NuxtLink>

    <div v-if="loading && !work" class="text-default-400 flex items-center justify-center py-20">
      <KunIcon name="lucide:loader-circle" class="size-6 animate-spin" />
    </div>
    <div v-else-if="!work" class="text-danger flex items-center justify-center py-20">作品不存在（404）</div>

    <template v-else>
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-foreground text-2xl font-bold">{{ work.work.display_name }}</h1>
        <span class="text-default-400 text-sm tabular-nums">#{{ work.work.id }}</span>
        <span class="text-default-500 text-sm">{{ MEDIUM_LABEL[work.work.medium_id] ?? work.work.medium_id }}</span>
        <span :class="`text-${CONTENT_RATING_COLOR[work.work.content_rating]}`" class="text-sm font-medium">
          {{ CONTENT_RATING_LABEL[work.work.content_rating] ?? work.work.content_rating }}
        </span>
        <span class="text-default-500 text-sm">{{ WORK_STATUS_LABEL[work.work.status] ?? work.work.status }}</span>
        <span v-if="work.work.site" class="text-success text-sm">认领 · {{ work.work.site }}</span>
        <span v-else class="text-default-400 text-sm">未认领</span>
      </div>

      <!-- 锚溯源卡置顶：机器可见性优先于美观 -->
      <KunCard class="p-5">
        <h2 class="text-foreground mb-1 text-lg font-semibold">锚溯源卡</h2>
        <p class="text-default-400 mb-4 text-xs">每条 ref 原样：source / tier / matched_by 规则串（等宽字体，不美化）。</p>
        <div v-if="!provenance.length" class="text-default-400 text-sm">无外部锚</div>
        <div class="space-y-2">
          <div
            v-for="(a, i) in provenance"
            :key="i"
            class="border-default-200 flex flex-wrap items-center gap-x-4 gap-y-1 rounded-md border p-3 font-mono text-xs"
          >
            <span class="text-foreground font-semibold">{{ a.source }}</span>
            <span class="text-default-600">{{ a.external_id }}</span>
            <span :class="`text-${LINK_KIND_COLOR[a.link_kind]}`">{{ LINK_KIND_LABEL[a.link_kind] }}</span>
            <span class="text-default-400">release #{{ a.release }}</span>
            <span v-if="a.matched_by" class="text-default-500">matched_by: {{ a.matched_by }}</span>
          </div>
        </div>
      </KunCard>

      <div class="grid gap-6 lg:grid-cols-2">
        <KunCard class="p-5">
          <h2 class="text-foreground mb-3 text-lg font-semibold">标题</h2>
          <div v-for="(t, i) in work.titles" :key="i" class="border-default-200 flex items-baseline gap-3 border-b py-2 text-sm last:border-0">
            <span class="text-default-400 w-20 shrink-0 text-xs">{{ TITLE_KIND_LABEL[t.kind] ?? t.kind }}</span>
            <span class="text-foreground">{{ t.title }}</span>
            <span class="text-default-400 text-xs">{{ t.lang }}</span>
          </div>
        </KunCard>

        <KunCard class="p-5">
          <h2 class="text-foreground mb-3 text-lg font-semibold">发行</h2>
          <div v-for="rel in work.releases" :key="rel.id" class="border-default-200 flex items-center gap-3 border-b py-2 text-sm last:border-0">
            <span class="text-default-400 text-xs tabular-nums">#{{ rel.id }}</span>
            <span class="text-foreground">{{ fuzzyDate(rel) }}</span>
            <span class="text-default-500 text-xs">{{ rel.anchors.length }} 锚</span>
          </div>
        </KunCard>

        <KunCard v-if="work.labels.length" class="p-5">
          <h2 class="text-foreground mb-3 text-lg font-semibold">厂牌归属</h2>
          <div v-for="l in work.labels" :key="l.label_id" class="flex items-center gap-3 py-1 text-sm">
            <NuxtLink class="text-primary" :to="`/catalog-browser/label/${l.label_id}`">{{ l.display_name }}</NuxtLink>
            <span class="text-default-400 text-xs">{{ LABEL_KIND_LABEL[l.label_kind] ?? l.label_kind }}</span>
            <span class="text-default-500 text-xs">归属 · {{ ATTRIBUTION_KIND_LABEL[l.kind] ?? l.kind }}</span>
          </div>
        </KunCard>

        <KunCard v-if="credits && credits.groups.length" class="p-5">
          <h2 class="text-foreground mb-3 text-lg font-semibold">署名（按 role）</h2>
          <div v-for="g in credits.groups" :key="g.role_id" class="mb-3 last:mb-0">
            <p class="text-default-500 mb-1 text-xs">{{ g.role_name || g.role_key }}</p>
            <div class="flex flex-wrap gap-x-4 gap-y-1">
              <span v-for="cr in g.credits" :key="cr.credit_name_id" class="text-foreground text-sm">
                {{ cr.name }}
                <span v-if="cr.character" class="text-default-400 text-xs">（{{ cr.character }}）</span>
                <span v-if="cr.source" class="text-default-300 text-xs">·{{ cr.source }}</span>
              </span>
            </div>
          </div>
        </KunCard>
      </div>
    </template>
  </div>
</template>
