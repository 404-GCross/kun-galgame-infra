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
  intro?: { lang: string; intro: string }[]
  claimed_by?: { site?: string; work_id?: number } | null
}
interface GalAggregate {
  names?: Record<string, string | null>
  images?: { banner?: { url?: string } }
  intro?: Record<string, string | null>
  scores?: {
    vndb?: { rating?: number; vote_count?: number; url?: string } | null
    bangumi?: { score?: number; rank?: number; url?: string } | null
    eg?: { median?: number; count?: number; url?: string } | null
  }
}

const route = useRoute()
const workId = computed(() => Number(route.params.id))
const nsfw = computed(() => route.query.nsfw === '1')

const apiKey = ref('')
const loading = ref(true)
const error = ref('')
const work = ref<WorkDetail | null>(null)
const gal = ref<GalAggregate | null>(null)

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
        include: 'intro,scores,covers',
        content_limit: nsfw.value ? 'all' : 'sfw'
      }).catch(() => null)
      gal.value = (g?.data as GalAggregate) ?? null
    }
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

// The wiki-era long intro carries literal backslash+newline pairs — normalize.
const introText = computed(() => {
  const zh = pickLocale(gal.value?.intro, ['zh-cn', 'zh_cn', 'zh-tw'])
  if (zh) return zh.replace(/\\\n/g, '\n')
  const list = work.value?.intro ?? []
  const pick =
    list.find((i) => i.lang.toLowerCase().startsWith('zh')) ??
    list.find((i) => i.lang === 'ja') ??
    list[0]
  return pick ? pick.intro.replace(/\\\n/g, '\n') : null
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
      <div
        v-if="banner"
        class="overflow-hidden rounded-2xl border border-default-200"
      >
        <KunImageNative
          :src="banner"
          :alt="titleMain"
          loading="eager"
          class-name="max-h-80 w-full object-cover"
        />
      </div>

      <header class="space-y-2">
        <h1 class="text-3xl font-bold tracking-tight text-foreground">
          {{ titleMain }}
        </h1>
        <p v-if="titleJa" class="text-lg text-default-500">{{ titleJa }}</p>
        <p v-if="titleEn" class="text-sm text-default-400">{{ titleEn }}</p>
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
          <KunChip
            v-for="l in work.labels ?? []"
            :key="`l-${l.id}`"
            color="primary"
            variant="flat"
            size="sm"
          >
            {{ l.display_name }}
          </KunChip>
          <KunChip
            v-for="sr in work.series ?? []"
            :key="`s-${sr.id}`"
            color="secondary"
            variant="flat"
            size="sm"
          >
            系列 · {{ sr.name }}（{{ sr.member_count }} 部）
          </KunChip>
        </div>
      </header>

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

      <section
        v-if="(work.popularity ?? []).length"
        class="flex flex-wrap gap-2"
      >
        <KunChip
          v-for="p in work.popularity"
          :key="`${p.source}-${p.metric}`"
          color="default"
          variant="flat"
          size="sm"
        >
          {{ p.source }} · {{ p.metric }} {{ fmt(p.value) }}
        </KunChip>
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

      <section v-if="introText">
        <h2 class="mb-2 text-lg font-semibold text-foreground">简介</h2>
        <p
          class="whitespace-pre-line text-sm leading-relaxed text-default-500"
        >
          {{ introText }}
        </p>
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
            class="overflow-hidden rounded-lg border border-default-200 bg-default-100"
          >
            <KunImageNative
              :src="sh.url"
              :alt="`截图 ${i + 1}`"
              loading="lazy"
              class-name="aspect-video w-full object-cover"
            />
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
                <span class="truncate text-sm font-medium text-foreground">
                  {{ c.name }}
                </span>
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
                class="mt-0.5 truncate text-xs text-default-400"
              >
                CV {{ c.voices.map((v) => v.name).join(' / ') }}
              </p>
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
              <KunChip
                v-for="cr in g.credits"
                :key="`${g.role_key}-${cr.id}-${cr.character ?? ''}`"
                color="default"
                variant="flat"
                size="sm"
              >
                {{ cr.name
                }}<template v-if="cr.character">（{{ cr.character }}）</template>
              </KunChip>
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
        本页由 NextMoe 开放 API 实时渲染，仅两次调用：
        <code class="font-mono">/v1/catalog/works/{id}</code>（聚合 facet +
        逐源归因）与
        <code class="font-mono">/v1/galgame/{gid}</code>（跨面聚合：banner /
        多语言名 / 长简介 / 三源评分链接）。同样的数据，你的应用也拿得到 ——
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
  </div>
</template>
