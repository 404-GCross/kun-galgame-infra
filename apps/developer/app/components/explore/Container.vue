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

onMounted(() => {
  apiKey.value = sessionStorage.getItem('explore_api_key') ?? ''
})
watch(apiKey, (v) => sessionStorage.setItem('explore_api_key', v))

const relay = async (path: string, query: Record<string, string>) => {
  const qs = new URLSearchParams(query).toString()
  return await $fetch<{ code: number; message: string; data: unknown }>(
    `/explore/${path}?${qs}`,
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

const rawJson = computed(() =>
  detail.value ? JSON.stringify(detail.value, null, 2) : ''
)
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
        <label
          for="explore-nsfw"
          class="flex items-center gap-2 pb-2 text-sm text-default-500"
        >
          <input id="explore-nsfw" v-model="nsfw" type="checkbox" class="size-4" />
          含 R18
        </label>
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
        <details class="rounded-lg border border-default-200">
          <summary class="cursor-pointer px-4 py-2 text-sm text-default-500">
            原始 JSON
          </summary>
          <pre class="max-h-96 overflow-auto p-4 text-xs leading-relaxed text-default-500"><code>{{ rawJson }}</code></pre>
        </details>
      </KunCard>
    </template>
  </div>
</template>
