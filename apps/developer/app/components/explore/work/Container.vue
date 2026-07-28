<script setup lang="ts">
// Showcase work-detail page: what a real product page looks like when rendered
// straight off the public API. Exactly two calls — the catalog work (aggregate
// facets + per-source attribution, include=relations,credits) and, when the
// work is claimed, the galgame aggregate (banner / localized names / long
// intro / cross-source score links). Same sessionStorage key as /explore;
// client-side fetch, so SSR serves a loading shell.
useSeoMeta({ title: '作品预览', robots: 'noindex' })

interface Img {
  url: string
  sexual?: number
  violence?: number
  source?: string
  kind?: string
  portrait_pinned?: boolean
}
interface Trait {
  id: number
  name: string
  group?: string
  sexual?: boolean
  spoiler?: number
  lie?: boolean
}
interface Character {
  id: number
  name: string
  kind?: string
  spoiler?: number
  image?: string
  voices?: { id: number; name: string }[]
}
interface CreditGroup {
  role_key: string
  role_name: string
  credits: { id: number; name: string; lang?: string; character?: string | null }[]
}
interface WorkDetail {
  id: number
  display_name?: string
  content_rating?: string
  release_date?: string
  olang?: string
  covers?: Img[]
  screenshots?: Img[]
  tags?: { name: string; source?: string }[]
  characters?: Character[]
  refs?: { source: string; external_id: string }[]
  releases?: {
    id: number
    kind?: string
    date?: string
    title?: string
    lang?: string
    platforms?: string[]
  }[]
  ratings?: { source: string; score: number; vote_count?: number; rank?: number }[]
  playtimes?: { source: string; minutes: number; vote_count?: number }[]
  popularity?: { source: string; metric: string; value: number }[]
  credits?: CreditGroup[]
  relations?: {
    relation_type?: string
    phrase?: string
    work?: { id: number; display_name?: string; content_rating?: string }
  }[]
  labels?: { id: number; display_name: string; kind?: string }[]
  series?: { id: number; name: string; member_count?: number }[]
  intro?: { lang: string; intro: string; machine?: boolean; source?: string }[]
  titles?: { kind?: string; lang?: string; latin?: string; title: string }[]
  claimed_by?: { site?: string; work_id?: number } | null
}
interface GalAggregate {
  names?: Record<string, string | null>
  images?: { banner?: { url?: string }; portrait?: { url?: string } }
  intro?: Record<string, string | null>
  scores?: {
    vndb?: { rating?: number; vote_count?: number; url?: string } | null
    bangumi?: { score?: number; rank?: number; url?: string } | null
    eg?: { median?: number; count?: number; url?: string } | null
  }
  aliases?: string[]
  links?: { id: number; name: string; link: string; source?: string }[]
  meta?: Record<string, unknown>
}

const route = useRoute()
const workId = computed(() => Number(route.params.id))
const nsfw = computed(() => route.query.nsfw === '1')

const apiKey = ref('')
const loading = ref(true)
const error = ref('')
const work = ref<WorkDetail | null>(null)
const gal = ref<GalAggregate | null>(null)
const charTraits = ref<Record<number, Trait[]>>({})
const entityTarget = ref<{
  kind: 'characters' | 'names' | 'labels' | 'works'
  id: number
} | null>(null)

const relay = async (path: string, query: Record<string, string>) => {
  const qs = new URLSearchParams(query).toString()
  return await $fetch<{ code: number; message: string; data: unknown }>(
    `/relay/${path}?${qs}`,
    { headers: { Authorization: `Bearer ${apiKey.value.trim()}` } }
  )
}

const load = async () => {
  loading.value = true
  error.value = ''
  work.value = null
  gal.value = null
  charTraits.value = {}
  entityTarget.value = null
  try {
    const resp = await relay(`v1/catalog/works/${workId.value}`, {
      include: 'relations,credits',
      ...(nsfw.value && { nsfw: '1' })
    })
    work.value = (resp.data as WorkDetail) ?? null
    const cb = work.value?.claimed_by
    if (
      cb &&
      typeof cb.work_id === 'number' &&
      String(cb.site ?? '').includes('galgame')
    ) {
      const g = await relay(`v1/galgame/${cb.work_id}`, {
        include: 'intro,scores,covers,links,meta',
        content_limit: nsfw.value ? 'all' : 'sfw'
      }).catch(() => null)
      gal.value = (g?.data as GalAggregate) ?? null
    }
    // Per-character traits: one call per DISPLAYED character (cap 12,
    // concurrent, failures silent). spoilers=0 keeps the page spoiler-safe;
    // sexual-family traits only appear when the nsfw gate is on (server-side).
    const visible = (work.value?.characters ?? [])
      .filter((c) => (c.spoiler ?? 0) === 0)
      .slice(0, 12)
    const traitResults = await Promise.allSettled(
      visible.map(async (c) => {
        const r = await relay(`v1/catalog/characters/${c.id}`, {
          spoilers: '0',
          ...(nsfw.value && { nsfw: '1' })
        })
        return { id: c.id, data: r.data as { traits?: Trait[] } | null }
      })
    )
    const tm: Record<number, Trait[]> = {}
    for (const r of traitResults)
      if (r.status === 'fulfilled' && r.value.data?.traits?.length)
        tm[r.value.id] = r.value.data.traits
    charTraits.value = tm
  } catch (e) {
    const err = e as { data?: { message?: string }; statusCode?: number }
    error.value = err.data?.message ?? `请求失败（${err.statusCode ?? '网络错误'}）`
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  apiKey.value = sessionStorage.getItem('explore_api_key') ?? ''
  if (apiKey.value) load()
  else loading.value = false
})

// Relation cards / modal rows navigate to another /explore/work/{id}; Nuxt
// REUSES the component (same route record), so onMounted won't re-fire —
// reload whenever the path (or the nsfw query) changes.
watch(
  () => route.fullPath,
  () => {
    if (route.params.id && apiKey.value) load()
  }
)

const pickLocale = (
  m: Record<string, string | null> | undefined,
  keys: string[]
) => {
  if (!m) return null
  for (const k of keys) {
    const v = m[k]
    if (typeof v === 'string' && v) return v
  }
  return null
}

const titleMain = computed(
  () =>
    pickLocale(gal.value?.names, ['zh-cn', 'zh_cn', 'zh-tw']) ??
    work.value?.display_name ??
    `#${workId.value}`
)
const titleJa = computed(() => {
  const ja = pickLocale(gal.value?.names, ['ja-jp', 'ja'])
  return ja && ja !== titleMain.value ? ja : null
})
const titleEn = computed(() => pickLocale(gal.value?.names, ['en-us', 'en']))

const safeImg = (c: Img) =>
  nsfw.value || ((c.sexual ?? 0) === 0 && (c.violence ?? 0) === 0)

const banner = computed(
  () =>
    gal.value?.images?.banner?.url ??
    work.value?.covers?.find(safeImg)?.url ??
    null
)
const portrait = computed(() => gal.value?.images?.portrait?.url ?? null)

// Every intro variant from BOTH faces, language-labeled and provenance-tagged.
// work.intro[] carries an honest `machine` flag (MT provenance); the galgame
// face intro map is wiki-curated. Literal backslash+newline pairs normalized.
const LOCALE_LABEL: Record<string, string> = {
  'zh-cn': '中文',
  zh_cn: '中文',
  'zh-tw': '繁體中文',
  'zh-Hans': '中文',
  'ja-jp': '日本語',
  ja: '日本語',
  'en-us': 'English',
  en: 'English'
}
interface IntroVariant {
  label: string
  text: string
  machine: boolean
  source?: string
}
const introVariants = computed<IntroVariant[]>(() => {
  const out: IntroVariant[] = []
  const gi = gal.value?.intro
  if (gi)
    for (const [k, v] of Object.entries(gi))
      if (typeof v === 'string' && v)
        out.push({
          label: LOCALE_LABEL[k] ?? k,
          text: v.replace(/\\\n/g, '\n'),
          machine: false,
          source: 'galgame_wiki'
        })
  for (const i of work.value?.intro ?? [])
    out.push({
      label: LOCALE_LABEL[i.lang] ?? i.lang,
      text: i.intro.replace(/\\\n/g, '\n'),
      machine: i.machine === true,
      source: i.source
    })
  return out
})
const introIdx = ref(0)
watch(
  introVariants,
  (v) => {
    const zh = v.findIndex((x) => x.label.includes('中文'))
    introIdx.value = zh >= 0 ? zh : 0
  },
  { immediate: true }
)
const activeIntro = computed(() => introVariants.value[introIdx.value] ?? null)

const aliasList = computed(() => (gal.value?.aliases ?? []).slice(0, 12))
const aliasRest = computed(
  () => Math.max(0, (gal.value?.aliases?.length ?? 0) - 12)
)
const coverList = computed(() =>
  (work.value?.covers ?? []).filter(safeImg).slice(0, 8)
)
const linkList = computed(() => (gal.value?.links ?? []).slice(0, 12))
const galView = computed(() => {
  const v = gal.value?.meta?.view
  return typeof v === 'number' ? v : null
})

const TAG_CAP = 40
const tags = computed(() => (work.value?.tags ?? []).slice(0, TAG_CAP))
const tagRest = computed(
  () => Math.max(0, (work.value?.tags?.length ?? 0) - TAG_CAP)
)

const shots = computed(() =>
  (work.value?.screenshots ?? []).filter(safeImg).slice(0, 8)
)

const chars = computed(() =>
  (work.value?.characters ?? [])
    .filter((c) => (c.spoiler ?? 0) === 0)
    .slice(0, 12)
)
const charsHidden = computed(
  () => (work.value?.characters ?? []).filter((c) => (c.spoiler ?? 0) > 0).length
)

const releases = computed(() => (work.value?.releases ?? []).slice(0, 10))
const releaseRest = computed(
  () => Math.max(0, (work.value?.releases?.length ?? 0) - 10)
)

const refLink = (source: string) => {
  const s = gal.value?.scores
  if (source === 'vndb') return s?.vndb?.url ?? null
  if (source === 'bangumi') return s?.bangumi?.url ?? null
  return null
}

const fmt = (n: number) => n.toLocaleString()

// Normalize a source-native score to 0-100 for the bar: EG is already 0-100,
// vndb / bangumi are 10-point scales.
const scorePct = (r: { score: number }) =>
  Math.max(0, Math.min(100, r.score > 10 ? r.score : r.score * 10))

// Popularity rows grouped per source, bar-scaled to the group max — bgm's five
// collection buckets (wish/collect/doing/hold/drop) become a real distribution
// chart; dlsite counters chart the same way.
const METRIC_LABEL: Record<string, string> = {
  wish: '想玩',
  collect: '玩过',
  doing: '在玩',
  hold: '搁置',
  drop: '抛弃',
  dl_count: '下载数',
  wishlist: '愿望单',
  review_count: '评论数'
}
const popGroups = computed(() => {
  const by = new Map<string, { metric: string; value: number }[]>()
  for (const pp of work.value?.popularity ?? []) {
    const arr = by.get(pp.source) ?? []
    arr.push({ metric: pp.metric, value: pp.value })
    by.set(pp.source, arr)
  }
  return [...by.entries()].map(([source, rows]) => ({
    source,
    max: Math.max(...rows.map((r) => r.value), 1),
    rows
  }))
})
</script>

<template>
  <div class="mx-auto w-full max-w-5xl space-y-8 px-4 py-8 md:px-6">
    <NuxtLink
      to="/explore"
      class="inline-flex items-center gap-1.5 text-sm text-default-500 transition-colors hover:text-foreground"
    >
      <KunIcon name="lucide:arrow-left" class="size-4" />
      返回数据浏览
    </NuxtLink>

    <KunCard v-if="!apiKey && !loading" content-class="p-10">
      <p class="text-center text-default-500">
        缺少 API key —— 先去
        <NuxtLink to="/explore" class="text-primary hover:underline">
          数据浏览
        </NuxtLink>
        填入你的 key，再从详情卡进入本页。
      </p>
    </KunCard>

    <KunCard v-else-if="loading" content-class="p-10">
      <p class="text-center text-default-400">正在从开放 API 拉取全量数据…</p>
    </KunCard>

    <div
      v-else-if="error"
      class="rounded-lg bg-danger-50 p-3 text-sm text-danger"
    >
      {{ error }}
    </div>

    <template v-else-if="work">
      <section
        v-if="banner"
        class="relative overflow-hidden rounded-2xl border border-default-200"
      >
        <KunImageNative
          :src="banner"
          :alt="titleMain"
          loading="eager"
          class-name="max-h-96 min-h-56 w-full object-cover"
        />
        <!-- Solid-color scrim (palette color + opacity, no gradients). -->
        <div
          class="absolute inset-x-0 bottom-0 flex items-end gap-4 bg-background/85 p-4 backdrop-blur md:p-5"
        >
          <div
            v-if="portrait"
            class="hidden w-20 shrink-0 overflow-hidden rounded-lg border border-default-200 sm:block md:w-24"
          >
            <KunImageNative
              :src="portrait"
              :alt="titleMain"
              loading="eager"
              class-name="aspect-[3/4] w-full object-cover"
            />
          </div>
          <div class="min-w-0">
            <h1
              class="truncate text-2xl font-bold tracking-tight text-foreground md:text-3xl"
            >
              {{ titleMain }}
            </h1>
            <p v-if="titleJa" class="truncate text-base text-default-500">
              {{ titleJa }}
            </p>
            <p v-if="titleEn" class="truncate text-xs text-default-400">
              {{ titleEn }}
            </p>
          </div>
        </div>
      </section>

      <header class="space-y-2">
        <template v-if="!banner">
          <h1 class="text-3xl font-bold tracking-tight text-foreground">
            {{ titleMain }}
          </h1>
          <p v-if="titleJa" class="text-lg text-default-500">{{ titleJa }}</p>
          <p v-if="titleEn" class="text-sm text-default-400">{{ titleEn }}</p>
        </template>
        <div class="flex flex-wrap items-center gap-2 pt-1">
          <KunChip
            v-if="work.content_rating === 'r18'"
            color="danger"
            variant="flat"
            size="sm"
          >
            R18
          </KunChip>
          <KunChip
            v-if="work.release_date"
            color="default"
            variant="flat"
            size="sm"
          >
            {{ work.release_date }}
          </KunChip>
          <KunChip v-if="work.olang" color="default" variant="flat" size="sm">
            原语言 {{ work.olang }}
          </KunChip>
          <button
            v-for="l in work.labels ?? []"
            :key="`l-${l.id}`"
            type="button"
            @click="entityTarget = { kind: 'labels', id: l.id }"
          >
            <KunChip color="primary" variant="flat" size="sm">
              {{ l.display_name }}
            </KunChip>
          </button>
          <KunChip
            v-for="sr in work.series ?? []"
            :key="`s-${sr.id}`"
            color="secondary"
            variant="flat"
            size="sm"
          >
            系列 · {{ sr.name }}（{{ sr.member_count }} 部）
          </KunChip>
          <KunChip v-if="galView !== null" color="default" variant="flat" size="sm">
            {{ fmt(galView) }} 次浏览
          </KunChip>
        </div>
      </header>

      <section v-if="(work.titles ?? []).length || aliasList.length">
        <h2 class="mb-2 text-lg font-semibold text-foreground">名称与别名</h2>
        <div
          v-if="(work.titles ?? []).length"
          class="mb-2 space-y-1 text-sm text-default-500"
        >
          <p v-for="(t, i) in work.titles" :key="`t-${i}`">
            <KunChip color="default" variant="flat" size="xs">
              {{ t.kind ?? 'title' }}<template v-if="t.lang"> · {{ t.lang }}</template>
            </KunChip>
            <span class="ml-2 text-foreground">{{ t.title }}</span>
            <span v-if="t.latin" class="ml-2 text-xs text-default-400">
              {{ t.latin }}
            </span>
          </p>
        </div>
        <div v-if="aliasList.length" class="flex flex-wrap gap-1.5">
          <KunChip
            v-for="a in aliasList"
            :key="a"
            color="default"
            variant="flat"
            size="xs"
          >
            {{ a }}
          </KunChip>
          <span v-if="aliasRest" class="text-xs text-default-400">
            +{{ aliasRest }}
          </span>
        </div>
      </section>

      <section
        v-if="(work.ratings ?? []).length || (work.playtimes ?? []).length"
        class="grid grid-cols-2 gap-3 md:grid-cols-4"
      >
        <div
          v-for="r in work.ratings ?? []"
          :key="`r-${r.source}`"
          class="rounded-xl border border-default-200 bg-content1 px-4 py-3"
        >
          <p class="text-xs uppercase tracking-wide text-default-400">
            {{ r.source }}
          </p>
          <p class="mt-1 text-2xl font-bold text-foreground">{{ r.score }}</p>
          <p class="text-xs text-default-400">
            {{ fmt(r.vote_count ?? 0) }} 票<template v-if="r.rank">
              · rank {{ fmt(r.rank) }}</template
            >
          </p>
          <div
            class="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-default-200"
          >
            <div
              class="h-full rounded-full bg-primary"
              :style="{ width: `${scorePct(r)}%` }"
            />
          </div>
        </div>
        <div
          v-for="p in work.playtimes ?? []"
          :key="`p-${p.source}`"
          class="rounded-xl border border-default-200 bg-content1 px-4 py-3"
        >
          <p class="text-xs uppercase tracking-wide text-default-400">
            {{ p.source }} · 时长
          </p>
          <p class="mt-1 text-2xl font-bold text-foreground">
            {{ Math.round(p.minutes / 60) }}
            <span class="text-sm font-normal">小时</span>
          </p>
          <p class="text-xs text-default-400">{{ fmt(p.minutes) }} 分钟</p>
        </div>
      </section>

      <section v-if="popGroups.length">
        <h2 class="mb-2 text-lg font-semibold text-foreground">热度分布</h2>
        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <div
            v-for="g in popGroups"
            :key="g.source"
            class="rounded-xl border border-default-200 bg-content1 p-4"
          >
            <p class="text-xs uppercase tracking-wide text-default-400">
              {{ g.source }}
            </p>
            <div class="mt-2 space-y-1.5">
              <div
                v-for="r in g.rows"
                :key="r.metric"
                class="flex items-center gap-2"
              >
                <span class="w-14 shrink-0 text-xs text-default-500">
                  {{ METRIC_LABEL[r.metric] ?? r.metric }}
                </span>
                <div
                  class="h-2 flex-1 overflow-hidden rounded-full bg-default-200"
                >
                  <div
                    class="h-full rounded-full bg-primary"
                    :style="{ width: `${(r.value / g.max) * 100}%` }"
                  />
                </div>
                <span
                  class="w-16 shrink-0 text-right text-xs font-medium text-foreground"
                >
                  {{ fmt(r.value) }}
                </span>
              </div>
            </div>
          </div>
        </div>
      </section>

      <section
        v-if="(work.refs ?? []).length"
        class="flex flex-wrap items-center gap-2"
      >
        <span class="text-xs text-default-400">外部标识</span>
        <template
          v-for="rf in work.refs"
          :key="`${rf.source}-${rf.external_id}`"
        >
          <a
            v-if="refLink(rf.source)"
            :href="refLink(rf.source) ?? undefined"
            target="_blank"
            rel="noopener"
          >
            <KunChip color="primary" variant="flat" size="sm">
              {{ rf.source }}:{{ rf.external_id }}
            </KunChip>
          </a>
          <KunChip v-else color="default" variant="flat" size="sm">
            {{ rf.source }}:{{ rf.external_id }}
          </KunChip>
        </template>
      </section>

      <section v-if="linkList.length" class="flex flex-wrap items-center gap-2">
        <span class="text-xs text-default-400">官方 / 外部链接</span>
        <a
          v-for="lk in linkList"
          :key="lk.id"
          :href="lk.link"
          target="_blank"
          rel="noopener"
        >
          <KunChip color="secondary" variant="flat" size="sm">
            {{ lk.name }}
          </KunChip>
        </a>
      </section>

      <section v-if="introVariants.length">
        <div class="mb-2 flex flex-wrap items-center gap-2">
          <h2 class="text-lg font-semibold text-foreground">简介</h2>
          <div class="flex flex-wrap gap-1">
            <KunButton
              v-for="(v, i) in introVariants"
              :key="`iv-${i}`"
              size="xs"
              :color="i === introIdx ? 'primary' : 'default'"
              variant="flat"
              @click="introIdx = i"
            >
              {{ v.label }}
              <template v-if="v.machine">· 机翻</template>
            </KunButton>
          </div>
        </div>
        <div
          v-if="activeIntro"
          class="space-y-2 rounded-xl border border-default-200 bg-content1 p-4"
        >
          <div class="flex flex-wrap gap-1.5">
            <KunChip
              v-if="activeIntro.source"
              color="default"
              variant="flat"
              size="xs"
            >
              来源 {{ activeIntro.source }}
            </KunChip>
            <KunChip
              v-if="activeIntro.machine"
              color="warning"
              variant="flat"
              size="xs"
            >
              机器翻译（API 原样标注 machine=true）
            </KunChip>
          </div>
          <p
            class="whitespace-pre-line text-sm leading-relaxed text-default-500"
          >
            {{ activeIntro.text }}
          </p>
        </div>
      </section>

      <section v-if="coverList.length">
        <h2 class="mb-2 text-lg font-semibold text-foreground">
          封面（{{ coverList.length }}）
        </h2>
        <div class="grid grid-cols-3 gap-3 sm:grid-cols-4 md:grid-cols-6">
          <div
            v-for="(cv, i) in coverList"
            :key="cv.url"
            class="relative overflow-hidden rounded-lg border border-default-200 bg-default-100 transition-colors hover:border-primary"
          >
            <KunImageNative
              :src="cv.url"
              :alt="`封面 ${i + 1}`"
              loading="lazy"
              class-name="aspect-[3/4] w-full object-cover"
            />
            <div class="absolute bottom-1 left-1 flex flex-wrap gap-1">
              <KunChip v-if="cv.source" color="default" variant="solid" size="xs">
                {{ cv.source }}
              </KunChip>
              <KunChip
                v-if="cv.portrait_pinned"
                color="primary"
                variant="solid"
                size="xs"
              >
                主图
              </KunChip>
              <KunChip
                v-if="(cv.sexual ?? 0) > 0"
                color="danger"
                variant="solid"
                size="xs"
              >
                s{{ cv.sexual }}
              </KunChip>
            </div>
          </div>
        </div>
      </section>

      <section v-if="tags.length">
        <h2 class="mb-2 text-lg font-semibold text-foreground">标签</h2>
        <div class="flex flex-wrap gap-1.5">
          <KunChip
            v-for="t in tags"
            :key="t.name"
            color="default"
            variant="flat"
            size="xs"
          >
            {{ t.name }}
          </KunChip>
          <span v-if="tagRest" class="text-xs text-default-400">
            +{{ tagRest }}
          </span>
        </div>
      </section>

      <section v-if="shots.length">
        <h2 class="mb-2 text-lg font-semibold text-foreground">截图</h2>
        <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
          <div
            v-for="(sh, i) in shots"
            :key="sh.url"
            class="relative overflow-hidden rounded-lg border border-default-200 bg-default-100 transition-colors hover:border-primary"
          >
            <KunImageNative
              :src="sh.url"
              :alt="`截图 ${i + 1}`"
              loading="lazy"
              class-name="aspect-video w-full object-cover"
            />
            <div class="absolute bottom-1 left-1 flex gap-1">
              <KunChip v-if="sh.source" color="default" variant="solid" size="xs">
                {{ sh.source }}
              </KunChip>
              <KunChip
                v-if="(sh.sexual ?? 0) > 0 || (sh.violence ?? 0) > 0"
                color="danger"
                variant="solid"
                size="xs"
              >
                s{{ sh.sexual ?? 0 }}/v{{ sh.violence ?? 0 }}
              </KunChip>
            </div>
          </div>
        </div>
      </section>

      <section v-if="chars.length">
        <h2 class="mb-2 text-lg font-semibold text-foreground">角色</h2>
        <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
          <div
            v-for="c in chars"
            :key="c.id"
            class="overflow-hidden rounded-xl border border-default-200 bg-content1"
          >
            <div v-if="c.image" class="aspect-[3/4] overflow-hidden bg-default-100">
              <KunImageNative
                :src="c.image"
                :alt="c.name"
                loading="lazy"
                class-name="h-full w-full object-cover"
              />
            </div>
            <div class="p-2.5">
              <div class="flex items-center gap-1.5">
                <button
                  type="button"
                  class="truncate text-sm font-medium text-foreground hover:text-primary hover:underline"
                  @click="entityTarget = { kind: 'characters', id: c.id }"
                >
                  {{ c.name }}
                </button>
                <KunChip
                  v-if="c.kind === 'main'"
                  color="primary"
                  variant="flat"
                  size="xs"
                >
                  主役
                </KunChip>
              </div>
              <p
                v-if="c.voices?.length"
                class="mt-0.5 flex flex-wrap items-center gap-1 text-xs text-default-400"
              >
                CV
                <button
                  v-for="v in c.voices"
                  :key="v.id"
                  type="button"
                  class="hover:text-primary hover:underline"
                  @click="entityTarget = { kind: 'names', id: v.id }"
                >
                  {{ v.name }}
                </button>
              </p>
              <div
                v-if="charTraits[c.id]?.length"
                class="mt-1.5 flex flex-wrap gap-1"
              >
                <KunChip
                  v-for="tr in (charTraits[c.id] ?? []).slice(0, 6)"
                  :key="tr.id"
                  :color="tr.sexual ? 'danger' : 'default'"
                  variant="flat"
                  size="xs"
                >
                  {{ tr.name }}
                </KunChip>
                <span
                  v-if="(charTraits[c.id]?.length ?? 0) > 6"
                  class="text-xs text-default-400"
                >
                  +{{ (charTraits[c.id]?.length ?? 0) - 6 }}
                </span>
              </div>
            </div>
          </div>
        </div>
        <p v-if="charsHidden" class="mt-2 text-xs text-default-400">
          另有 {{ charsHidden }} 位含剧透角色未显示（spoilers 参数可控）
        </p>
      </section>

      <section v-if="(work.credits ?? []).length">
        <h2 class="mb-2 text-lg font-semibold text-foreground">制作阵容</h2>
        <div class="space-y-3">
          <div v-for="g in work.credits" :key="g.role_key">
            <p class="text-xs font-medium text-default-400">
              {{ g.role_name }}
            </p>
            <div class="mt-1 flex flex-wrap gap-1.5">
              <button
                v-for="cr in g.credits"
                :key="`${g.role_key}-${cr.id}-${cr.character ?? ''}`"
                type="button"
                @click="entityTarget = { kind: 'names', id: cr.id }"
              >
                <KunChip color="default" variant="flat" size="sm">
                  {{ cr.name
                  }}<template v-if="cr.character">（{{ cr.character }}）</template>
                </KunChip>
              </button>
            </div>
          </div>
        </div>
      </section>

      <section v-if="releases.length">
        <h2 class="mb-2 text-lg font-semibold text-foreground">发行版本</h2>
        <KunCard content-class="p-0" class-name="overflow-hidden">
          <div
            v-for="rl in releases"
            :key="rl.id"
            class="flex flex-wrap items-center gap-2 border-b border-default-100 px-4 py-2.5 text-sm last:border-b-0"
          >
            <span class="font-mono text-xs text-default-400">{{
              rl.date ?? '—'
            }}</span>
            <span class="min-w-0 flex-1 truncate text-foreground">{{
              rl.title
            }}</span>
            <KunChip
              v-if="rl.kind && rl.kind !== 'complete'"
              color="warning"
              variant="flat"
              size="xs"
            >
              {{ rl.kind }}
            </KunChip>
            <KunChip v-if="rl.lang" color="default" variant="flat" size="xs">
              {{ rl.lang }}
            </KunChip>
            <span class="text-xs text-default-400">
              {{ (rl.platforms ?? []).join(' / ') }}
            </span>
          </div>
        </KunCard>
        <p v-if="releaseRest" class="mt-2 text-xs text-default-400">
          另有 {{ releaseRest }} 个版本未列出
        </p>
      </section>

      <section v-if="(work.relations ?? []).length">
        <h2 class="mb-2 text-lg font-semibold text-foreground">关联作品</h2>
        <div class="grid grid-cols-1 gap-2 md:grid-cols-2">
          <NuxtLink
            v-for="(rel, i) in work.relations"
            :key="`rel-${i}`"
            :to="`/explore/work/${rel.work?.id}${nsfw ? '?nsfw=1' : ''}`"
            class="group flex items-center gap-3 rounded-xl border border-default-200 bg-content1 px-4 py-3 transition-colors hover:border-primary"
          >
            <KunChip color="secondary" variant="flat" size="xs">
              {{ rel.phrase ?? rel.relation_type }}
            </KunChip>
            <span
              class="min-w-0 flex-1 truncate text-sm font-medium text-foreground"
            >
              {{ rel.work?.display_name ?? `#${rel.work?.id}` }}
            </span>
            <KunChip
              v-if="rel.work?.content_rating === 'r18'"
              color="danger"
              variant="flat"
              size="xs"
            >
              R18
            </KunChip>
            <KunIcon
              name="lucide:arrow-right"
              class="size-4 shrink-0 text-default-400 transition-transform group-hover:translate-x-0.5"
            />
          </NuxtLink>
        </div>
      </section>

      <p class="border-t border-default-200 pt-4 text-xs text-default-400">
        本页由 NextMoe 开放 API 实时渲染：
        <code class="font-mono">/v1/catalog/works/{id}</code>（聚合 facet +
        逐源归因）+
        <code class="font-mono">/v1/galgame/{gid}</code>（跨面聚合：banner /
        多语言名 / 多语言简介含机翻标注 / 别名 / 官方链接 / 三源评分）+
        逐角色 <code class="font-mono">/v1/catalog/characters/{id}</code>
        （traits）。同样的数据，你的应用也拿得到 ——
        <NuxtLink to="/docs" class="text-primary hover:underline">
          API 文档
        </NuxtLink>
        ·
        <NuxtLink to="/docs/example" class="text-primary hover:underline">
          实战示例
        </NuxtLink>
        。
      </p>
    </template>

    <ExploreEntityModal
      v-model="entityTarget"
      :api-key="apiKey"
      :nsfw="nsfw"
    />
  </div>
</template>
