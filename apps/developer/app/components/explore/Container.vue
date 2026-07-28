<script setup lang="ts">
// Interactive data browser: paste your own nm_ key (kept in sessionStorage,
// sent ONLY as the Authorization header through our GET-only relay — never
// persisted server-side), search works by title, click into the full detail.
// Search hits render typed (their shape is frozen); the detail renders a facet
// summary + the raw JSON, so the page never lies about fields it doesn't know.
useSeoMeta({ title: '数据浏览', robots: 'noindex' })

interface SearchHit {
  id: number
  entity_type: string
  name: string
  latin?: string | null
  content_rating?: string
  sources?: string[]
}

const apiKey = ref('')
const q = ref('')
const nsfw = ref(false)
const searching = ref(false)
const loadingDetail = ref(false)
const error = ref('')
const hits = ref<SearchHit[]>([])
const total = ref(0)
const detail = ref<Record<string, unknown> | null>(null)
const showRaw = ref(false)

// ── Entity panorama (fan-out) ──
// From ONE work, pull every linked entity's own full record — labels,
// characters, credited names, related works — each through its own public
// read face. This is the entity-centric read model made visible: one
// consistent archive per entity kind, aggregates + per-source attribution
// on every one of them.
interface ExpandedEntity {
  group: string
  id: number
  name: string
  raw: Record<string, unknown>
  showRaw: boolean
}
const expanded = ref<ExpandedEntity[]>([])
const expandNote = ref('')
const expanding = ref(false)
const EXPAND_CAP = 6

// Click an expanded entity card → its full record in the reusable modal.
const GROUP_KIND: Record<string, 'characters' | 'names' | 'labels' | 'works'> = {
  '厂牌 / 社团': 'labels',
  '角色': 'characters',
  '名义': 'names',
  '关联作品': 'works'
}
const entityTarget = ref<{
  kind: 'characters' | 'names' | 'labels' | 'works'
  id: number
} | null>(null)
const openEntity = (e: ExpandedEntity) => {
  const kind = GROUP_KIND[e.group]
  if (kind) entityTarget.value = { kind, id: e.id }
}

const idOf = (v: unknown): number | null => {
  if (v && typeof v === 'object' && 'id' in v) {
    const n = (v as { id?: unknown }).id
    return typeof n === 'number' ? n : null
  }
  return null
}

const expandAll = async () => {
  if (expanding.value || !detail.value) return
  expanding.value = true
  expandNote.value = ''
  expanded.value = []
  const d = detail.value
  const arr = (k: string): unknown[] => (Array.isArray(d[k]) ? (d[k] as unknown[]) : [])

  const jobs: {
    group: string
    id: number
    path: string
    query: Record<string, string>
  }[] = []
  const seen = new Set<string>()
  const gate = (): Record<string, string> =>
    nsfw.value ? { nsfw: '1' } : {}
  const push = (
    group: string,
    id: number | null,
    path: (i: number) => string,
    query: Record<string, string> = {}
  ) => {
    if (id === null || seen.has(`${group}:${id}`)) return
    seen.add(`${group}:${id}`)
    jobs.push({ group, id, path: path(id), query })
  }

  // Each entity kind is pulled with ITS OWN heaviest include set — the point
  // is the fullest single-entity record each face can serve.
  for (const l of arr('labels'))
    push('厂牌 / 社团', idOf(l), (i) => `v1/catalog/labels/${i}`, {
      include: 'works',
      ...gate()
    })
  for (const c of arr('characters'))
    push('角色', idOf(c), (i) => `v1/catalog/characters/${i}`, {
      include: 'works',
      spoilers: '2',
      ...gate()
    })
  for (const r of arr('credits')) {
    const inner = (r as { credits?: unknown[] }).credits
    if (Array.isArray(inner))
      for (const c of inner)
        push(
          '名义',
          idOf((c as { name?: unknown }).name) ?? idOf(c),
          (i) => `v1/catalog/names/${i}`,
          { include: 'credits', ...gate() }
        )
  }
  for (const r of arr('relations'))
    push(
      '关联作品',
      idOf((r as { work?: unknown }).work),
      (i) => `v1/catalog/works/${i}`,
      { include: 'relations,credits', ...gate() }
    )
  // Cross-face: the claimed galgame aggregate is the richest single record
  // in the system — intro/scores/covers/taxonomy/links/screenshots/series/meta.
  const cb = d.claimed_by as { site?: string; work_id?: number } | null
  if (cb && typeof cb.work_id === 'number' && String(cb.site ?? '').includes('galgame'))
    push('跨面 · Galgame 聚合', cb.work_id, (i) => `v1/galgame/${i}`, {
      include: 'intro,scores,covers,taxonomy,links,screenshots,series,meta',
      content_limit: nsfw.value ? 'all' : 'sfw'
    })

  const totals = new Map<string, number>()
  for (const j of jobs) totals.set(j.group, (totals.get(j.group) ?? 0) + 1)
  const taken = new Map<string, number>()
  const capped: typeof jobs = []
  for (const j of jobs) {
    const n = taken.get(j.group) ?? 0
    if (n < EXPAND_CAP) {
      taken.set(j.group, n + 1)
      capped.push(j)
    }
  }

  const results = await Promise.allSettled(
    capped.map(async (j) => {
      const resp = await relay(j.path, j.query)
      return { j, data: resp.data as Record<string, unknown> | null }
    })
  )
  let failed = 0
  for (const r of results) {
    if (r.status !== 'fulfilled' || !r.value.data) {
      failed++
      continue
    }
    const { j, data } = r.value
    expanded.value.push({
      group: j.group,
      id: j.id,
      name: entityName(data) ?? `#${j.id}`,
      raw: data,
      showRaw: false
    })
  }
  const notes: string[] = []
  for (const [g, total] of totals) {
    const took = taken.get(g) ?? 0
    if (total > took) notes.push(`${g} 仅取前 ${took}/${total}`)
  }
  if (failed) notes.push(`${failed} 个实体拉取失败`)
  expandNote.value = notes.join('；')
  expanding.value = false
}

const expandedGroups = computed(() => {
  const m = new Map<string, ExpandedEntity[]>()
  for (const e of expanded.value) {
    const list = m.get(e.group) ?? []
    list.push(e)
    m.set(e.group, list)
  }
  return [...m.entries()]
})

// Chip-sized facet overview of any entity record (arrays show their length).
const facetsOf = (obj: Record<string, unknown>) =>
  Object.entries(obj)
    .filter(([, v]) => (Array.isArray(v) ? v.length > 0 : v !== null && v !== ''))
    .map(([k, v]) => ({ key: k, count: Array.isArray(v) ? `${v.length}` : '' }))
    .slice(0, 12)

onMounted(() => {
  apiKey.value = sessionStorage.getItem('explore_api_key') ?? ''
})
watch(apiKey, (v) => sessionStorage.setItem('explore_api_key', v))

const relay = async (path: string, query: Record<string, string>) => {
  const qs = new URLSearchParams(query).toString()
  return await $fetch<{ code: number; message: string; data: unknown }>(
    `/relay/${path}?${qs}`,
    { headers: { Authorization: `Bearer ${apiKey.value.trim()}` } }
  )
}

const search = async () => {
  if (searching.value || !apiKey.value.trim()) return
  searching.value = true
  error.value = ''
  detail.value = null
  try {
    const resp = await relay('v1/catalog/search', {
      type: 'works',
      q: q.value,
      ...(nsfw.value && { nsfw: '1' })
    })
    const data = resp.data as { items?: SearchHit[]; total?: number } | null
    hits.value = data?.items ?? []
    total.value = data?.total ?? 0
  } catch (e) {
    hits.value = []
    const err = e as { data?: { message?: string }; statusCode?: number }
    error.value = err.data?.message ?? `请求失败（${err.statusCode ?? '网络错误'}）`
  } finally {
    searching.value = false
  }
}

const openDetail = async (id: number) => {
  if (loadingDetail.value) return
  loadingDetail.value = true
  error.value = ''
  try {
    const resp = await relay(`v1/catalog/works/${id}`, {
      include: 'relations,credits',
      ...(nsfw.value && { nsfw: '1' })
    })
    detail.value = (resp.data as Record<string, unknown>) ?? null
    expanded.value = []
    expandNote.value = ''
    showRaw.value = false
  } catch (e) {
    const err = e as { data?: { message?: string }; statusCode?: number }
    error.value = err.data?.message ?? `请求失败（${err.statusCode ?? '网络错误'}）`
  } finally {
    loadingDetail.value = false
  }
}

// Facet summary: for each key of the detail, how much is in it — an honest
// overview that works whatever shape the block has.
const facetSummary = computed(() => {
  if (!detail.value) return []
  return Object.entries(detail.value)
    .map(([k, v]) => ({
      key: k,
      kind: Array.isArray(v) ? `${v.length} 条` : v === null ? 'null' : typeof v
    }))
    .sort((a, b) => a.key.localeCompare(b.key))
})

</script>

<template>
  <div class="mx-auto w-full max-w-5xl space-y-6 px-4 py-10 md:px-6">
    <div>
      <h1 class="text-2xl font-bold text-foreground">数据浏览</h1>
      <p class="mt-1 text-sm text-default-500">
        用你自己的 API key 实时浏览开放数据（key 只存在本浏览器的
        sessionStorage，请求经本站 GET-only 中继转发，不落库）。还没有 key？去
        <NuxtLink to="/dashboard" class="text-primary hover:underline">控制台</NuxtLink>
        创建应用即得。
      </p>
    </div>

    <KunCard content-class="gap-4">
      <KunInput
        v-model="apiKey"
        label="API Key"
        type="password"
        placeholder="nm_live_…"
      />
      <div class="flex flex-wrap items-end gap-3">
        <div class="min-w-0 flex-1">
          <KunInput
            v-model="q"
            label="作品标题搜索"
            placeholder="如 SEQUEL / いろセカ"
            @keyup.enter="search"
          />
        </div>
        <div class="pb-2">
          <KunCheckBox v-model="nsfw" label="含 R18" />
        </div>
        <KunButton color="primary" :disabled="searching || !apiKey" @click="search">
          {{ searching ? '搜索中…' : '搜索' }}
        </KunButton>
      </div>
    </KunCard>

    <div v-if="error" class="rounded-lg bg-danger-50 p-3 text-sm text-danger">
      {{ error }}
    </div>

    <KunCard v-if="hits.length" content-class="p-0" class-name="overflow-hidden">
      <div class="border-b border-default-200 px-4 py-2 text-xs text-default-400">
        {{ total.toLocaleString() }} 个命中
      </div>
      <button
        v-for="h in hits"
        :key="h.id"
        type="button"
        class="flex w-full items-center gap-3 border-b border-default-100 px-4 py-3 text-left transition-colors last:border-b-0 hover:bg-default-100"
        @click="openDetail(h.id)"
      >
        <span class="min-w-0 flex-1 truncate text-sm font-medium text-foreground">
          {{ h.name }}
        </span>
        <KunChip v-if="h.content_rating === 'r18'" color="danger" variant="flat" size="xs">
          R18
        </KunChip>
        <KunChip color="default" variant="flat" size="xs">#{{ h.id }}</KunChip>
      </button>
    </KunCard>

    <KunCard v-if="loadingDetail" content-class="p-10">
      <p class="text-center text-default-400">加载详情…</p>
    </KunCard>

    <template v-if="detail && !loadingDetail">
      <KunCard content-class="gap-3">
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-lg font-semibold text-foreground">
            {{ (detail as { display_name?: string }).display_name ?? `#${(detail as { id?: number }).id}` }}
          </h2>
          <KunChip
            v-if="(detail as { content_rating?: string }).content_rating === 'r18'"
            color="danger"
            variant="flat"
            size="xs"
          >
            R18
          </KunChip>
          <KunButton
            size="sm"
            color="primary"
            variant="flat"
            @click="
              navigateTo(
                `/explore/work/${(detail as { id?: number }).id}${nsfw ? '?nsfw=1' : ''}`
              )
            "
          >
            预览详情页
          </KunButton>
        </div>
        <div class="grid grid-cols-2 gap-2 md:grid-cols-4">
          <div
            v-for="f in facetSummary"
            :key="f.key"
            class="rounded-lg border border-default-200 px-3 py-2"
          >
            <p class="truncate font-mono text-xs text-default-400">{{ f.key }}</p>
            <p class="text-sm font-medium text-foreground">{{ f.kind }}</p>
          </div>
        </div>
        <div class="rounded-lg border border-default-200">
          <KunButton
            variant="light"
            color="default"
            size="sm"
            @click="showRaw = !showRaw"
          >
            {{ showRaw ? '收起数据树' : '展开数据树' }}
          </KunButton>
          <div v-show="showRaw" class="max-h-96 overflow-auto p-4">
            <ExploreJsonTree :data="detail" />
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-3">
          <KunButton
            color="primary"
            variant="flat"
            :disabled="expanding"
            @click="expandAll"
          >
            {{ expanding ? '展开中…' : '展开实体全景' }}
          </KunButton>
          <span class="text-xs text-default-400">
            对每个关联实体（厂牌 / 角色 / 名义 / 关联作品）各打一次它自己的读面
            —— 实体为中心，每类实体一套一致的完整档案。
          </span>
        </div>
        <p v-if="expandNote" class="text-xs text-warning">{{ expandNote }}</p>

        <div
          v-for="[group, ents] in expandedGroups"
          :key="group"
          class="space-y-2"
        >
          <h3 class="text-sm font-semibold text-foreground">
            {{ group }}（{{ ents.length }}）
          </h3>
          <div class="grid grid-cols-1 gap-2 md:grid-cols-2">
            <div
              v-for="e in ents"
              :key="`${group}-${e.id}`"
              class="rounded-lg border border-default-200 px-3 py-2"
            >
              <div class="flex items-center justify-between gap-2">
                <button
                  type="button"
                  class="truncate text-sm font-medium text-foreground hover:text-primary hover:underline"
                  @click="openEntity(e)"
                >
                  {{ e.name }}
                </button>
                <KunChip color="default" variant="flat" size="xs">
                  #{{ e.id }}
                </KunChip>
              </div>
              <div class="mt-1.5 flex flex-wrap gap-1">
                <KunChip
                  v-for="f in facetsOf(e.raw)"
                  :key="f.key"
                  color="default"
                  variant="flat"
                  size="xs"
                >
                  {{ f.key }}{{ f.count ? ` ${f.count}` : '' }}
                </KunChip>
              </div>
              <KunButton
                variant="light"
                color="default"
                size="xs"
                class="mt-1.5"
                @click="e.showRaw = !e.showRaw"
              >
                {{ e.showRaw ? '收起数据树' : '数据树' }}
              </KunButton>
              <div v-if="e.showRaw" class="mt-1 max-h-64 overflow-auto rounded bg-default-100 p-2">
                <ExploreJsonTree :data="e.raw" />
              </div>
            </div>
          </div>
        </div>
      </KunCard>
    </template>

    <ExploreEntityModal
      v-model="entityTarget"
      :api-key="apiKey"
      :nsfw="nsfw"
    />
  </div>
</template>
