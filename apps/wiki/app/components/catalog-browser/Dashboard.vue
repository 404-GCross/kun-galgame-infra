<script setup lang="ts">
import type { CatalogStats } from '~/shared/types/catalog'
import {
  MEDIUM_LABEL,
  WORK_STATUS_LABEL,
  LINK_KIND_LABEL,
  LINK_KIND_COLOR,
  ATTRIBUTION_KIND_LABEL,
  CANDIDATE_STATUS_LABEL,
  PROPOSAL_STATUS_LABEL,
  LLM_TRUST_MAP,
  useCatalogAdminUrl
} from '~/constants/catalog'

const catalog = useCatalog()
const adminUrl = useCatalogAdminUrl()

const { data: stats, status } = await useAsyncData('catalog-stats', async () => {
  const r = await catalog.stats()
  return r.code === 0 ? (r.data as CatalogStats) : null
})
const loading = computed(() => status.value === 'pending')

const fmt = (n: number) => n.toLocaleString()

// Anchor source × tier: pivot the flat cells into rows (source) × columns
// (link_kind 0/1/2). One table says all of identity quality.
const tiers = [0, 1, 2]
const anchorTable = computed(() => {
  const s = stats.value
  if (!s) return []
  const bySource: Record<string, Record<number, number>> = {}
  for (const c of s.anchors_by_source_tier) {
    bySource[c.source] ??= {}
    bySource[c.source]![c.link_kind] = c.count
  }
  return Object.keys(bySource)
    .sort()
    .map((src) => ({
      source: src,
      cells: tiers.map((t) => bySource[src]![t] ?? 0),
      total: tiers.reduce((a, t) => a + (bySource[src]![t] ?? 0), 0)
    }))
})

const overviewCards = computed(() => {
  const s = stats.value
  if (!s) return []
  return [
    { label: 'works', value: s.works.total, icon: 'lucide:library' },
    { label: 'credit 名义', value: s.entities.credit_names, icon: 'lucide:users' },
    { label: '角色', value: s.entities.characters, icon: 'lucide:drama' },
    { label: '厂牌', value: s.entities.labels, icon: 'lucide:building-2' },
    { label: '归属边', value: s.attributions_by_kind.reduce((a, c) => a + c.count, 0), icon: 'lucide:link' },
    { label: 'probable 锚', value: s.queues.probable_refs, icon: 'lucide:git-pull-request-arrow' }
  ]
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-foreground text-2xl font-bold">catalog 数据浏览器</h1>
      <div class="flex gap-2">
        <NuxtLink to="/catalog-browser/search">
          <KunButton variant="light" color="primary" size="sm">
            <KunIcon name="lucide:search" class="size-4" />
            实体搜索
          </KunButton>
        </NuxtLink>
      </div>
    </div>
    <p class="text-default-500 text-sm">
      内部工具：暴露机器（分级溯源 / 门控 / 孤儿教义的运转状态）。只看不动——一切裁决在
      <a class="text-primary" :href="adminUrl" target="_blank" rel="noopener">apps/web admin 三桶</a>。
    </p>

    <div v-if="loading && !stats" class="text-default-400 flex items-center justify-center py-20">
      <KunIcon name="lucide:loader-circle" class="size-6 animate-spin" />
    </div>

    <div v-else-if="!stats" class="text-danger flex items-center justify-center py-20">
      加载失败（可能非 staff 或 catalog 未接线）
    </div>

    <template v-else>
      <!-- overview -->
      <div class="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
        <KunCard v-for="c in overviewCards" :key="c.label" class="p-4">
          <div class="flex items-center gap-3">
            <div class="bg-default-100 text-primary rounded-lg p-2">
              <KunIcon :name="c.icon" class="size-5" />
            </div>
            <div class="min-w-0">
              <p class="text-default-500 truncate text-xs">{{ c.label }}</p>
              <p class="text-foreground text-xl font-bold">{{ fmt(c.value) }}</p>
            </div>
          </div>
        </KunCard>
      </div>

      <!-- 孤儿率单列：person=0 如实（教义不是缺陷） -->
      <KunCard class="border-warning-200 p-5">
        <div class="flex flex-wrap items-baseline gap-x-6 gap-y-1">
          <span class="text-foreground font-semibold">孤儿名义</span>
          <span class="text-foreground text-lg font-bold">
            {{ fmt(stats.entities.orphan_credit_names) }} / {{ fmt(stats.entities.credit_names) }}
          </span>
          <span class="text-default-500 text-sm">
            person = {{ stats.entities.persons }}（尚未建立人物关联 —— 这是孤儿教义的运转状态，不是缺陷）
          </span>
        </div>
      </KunCard>

      <!-- 质量面板：锚分层交叉表（灵魂） -->
      <KunCard class="p-5">
        <h2 class="text-foreground mb-1 text-lg font-semibold">锚分层交叉表（source × tier）</h2>
        <p class="text-default-500 mb-4 text-xs">一张表说尽身份质量：每个来源的 exact / probable / related 锚计数。</p>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[32rem] text-sm">
            <thead>
              <tr class="text-default-500 border-default-200 border-b text-left">
                <th class="py-2 pr-4 font-medium">source</th>
                <th v-for="t in tiers" :key="t" class="px-3 py-2 text-right font-medium">
                  {{ LINK_KIND_LABEL[t] }}
                </th>
                <th class="py-2 pl-3 text-right font-medium">合计</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in anchorTable" :key="row.source" class="border-default-200 border-b">
                <td class="text-foreground py-2 pr-4 font-medium">{{ row.source }}</td>
                <td v-for="(v, i) in row.cells" :key="i" class="px-3 py-2 text-right tabular-nums">
                  <span :class="v > 0 ? `text-${LINK_KIND_COLOR[tiers[i]!]}` : 'text-default-300'">{{ fmt(v) }}</span>
                </td>
                <td class="text-foreground py-2 pl-3 text-right font-semibold tabular-nums">{{ fmt(row.total) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </KunCard>

      <div class="grid gap-6 lg:grid-cols-2">
        <!-- works 矩阵 -->
        <KunCard class="p-5">
          <h2 class="text-foreground mb-3 text-lg font-semibold">works 矩阵（媒介 × 认领 × status）</h2>
          <table class="w-full text-sm">
            <thead>
              <tr class="text-default-500 border-default-200 border-b text-left">
                <th class="py-2 font-medium">媒介</th>
                <th class="py-2 font-medium">认领</th>
                <th class="py-2 font-medium">status</th>
                <th class="py-2 text-right font-medium">count</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(cell, i) in stats.works.cells" :key="i" class="border-default-200 border-b">
                <td class="text-foreground py-2">{{ MEDIUM_LABEL[cell.medium_id] ?? cell.medium_id }}</td>
                <td class="py-2">
                  <span :class="cell.claimed ? 'text-success' : 'text-default-400'">
                    {{ cell.claimed ? '已认领' : '未认领' }}
                  </span>
                </td>
                <td class="text-default-500 py-2">{{ WORK_STATUS_LABEL[cell.status] ?? cell.status }}</td>
                <td class="text-foreground py-2 text-right tabular-nums">{{ fmt(cell.count) }}</td>
              </tr>
            </tbody>
          </table>
        </KunCard>

        <!-- 人审水位（跳 admin） -->
        <KunCard class="p-5">
          <div class="mb-3 flex items-center justify-between">
            <h2 class="text-foreground text-lg font-semibold">人审水位</h2>
            <a :href="adminUrl" target="_blank" rel="noopener">
              <KunButton variant="light" color="primary" size="sm">
                去 admin 处理
                <KunIcon name="lucide:external-link" class="size-4" />
              </KunButton>
            </a>
          </div>
          <dl class="space-y-2 text-sm">
            <div class="flex justify-between">
              <dt class="text-default-500">probable 锚（待确认）</dt>
              <dd class="text-foreground font-semibold tabular-nums">{{ fmt(stats.queues.probable_refs) }}</dd>
            </div>
            <div v-for="c in stats.queues.candidates_by_status" :key="'c' + c.status" class="flex justify-between">
              <dt class="text-default-500">候选 · {{ CANDIDATE_STATUS_LABEL[c.status] ?? c.status }}</dt>
              <dd class="text-foreground font-semibold tabular-nums">{{ fmt(c.count) }}</dd>
            </div>
            <div v-for="p in stats.queues.proposals_by_status" :key="'p' + p.status" class="flex justify-between">
              <dt class="text-default-500">合并提案 · {{ PROPOSAL_STATUS_LABEL[p.status] ?? p.status }}</dt>
              <dd class="text-foreground font-semibold tabular-nums">{{ fmt(p.count) }}</dd>
            </div>
            <div v-if="!stats.queues.proposals_by_status.length" class="flex justify-between">
              <dt class="text-default-500">合并提案</dt>
              <dd class="text-default-400 tabular-nums">0（如实）</dd>
            </div>
            <div class="flex justify-between">
              <dt class="text-default-500">驳回记录（永久保留）</dt>
              <dd class="text-foreground font-semibold tabular-nums">{{ fmt(stats.queues.rejections) }}</dd>
            </div>
          </dl>
        </KunCard>

        <!-- credits by source + 归属 by kind -->
        <KunCard class="p-5">
          <h2 class="text-foreground mb-3 text-lg font-semibold">credits 按源 · 归属边按类</h2>
          <div class="grid grid-cols-2 gap-6 text-sm">
            <div>
              <p class="text-default-500 mb-2 text-xs">credits by source</p>
              <div v-for="c in stats.credits_by_source" :key="c.key || 'user'" class="flex justify-between py-1">
                <span class="text-foreground">{{ c.key || '(user)' }}</span>
                <span class="text-default-600 tabular-nums">{{ fmt(c.count) }}</span>
              </div>
            </div>
            <div>
              <p class="text-default-500 mb-2 text-xs">归属边 by kind</p>
              <div v-for="a in stats.attributions_by_kind" :key="a.kind" class="flex justify-between py-1">
                <span class="text-foreground">{{ ATTRIBUTION_KIND_LABEL[a.kind] ?? a.kind }}</span>
                <span class="text-default-600 tabular-nums">{{ fmt(a.count) }}</span>
              </div>
            </div>
          </div>
        </KunCard>

        <!-- LLM bid 判定 + 新鲜度 -->
        <KunCard class="p-5">
          <h2 class="text-foreground mb-3 text-lg font-semibold">LLM bid 判定 · 数据新鲜度</h2>
          <div class="grid grid-cols-2 gap-6 text-sm">
            <div>
              <p class="text-default-500 mb-2 text-xs">bid 身份判定</p>
              <div v-for="v in stats.llm_bid_verdicts" :key="v.key" class="flex justify-between py-1">
                <span class="text-foreground">{{ v.key }}</span>
                <span class="text-default-600 tabular-nums">{{ fmt(v.count) }}</span>
              </div>
              <p v-if="!stats.llm_bid_verdicts.length" class="text-default-400">src_llm 未在库</p>
            </div>
            <div>
              <p class="text-default-500 mb-2 text-xs">各源锚 max(created_at)</p>
              <div v-for="f in stats.source_freshness" :key="f.source" class="flex justify-between gap-2 py-1">
                <span class="text-foreground">{{ f.source }}</span>
                <span class="text-default-500 text-xs tabular-nums">{{ f.latest_ref?.slice(0, 10) || '—' }}</span>
              </div>
            </div>
          </div>
        </KunCard>
      </div>

      <!-- LLM 信任地图（静态） -->
      <KunCard class="p-5">
        <h2 class="text-foreground mb-1 text-lg font-semibold">LLM 信任地图</h2>
        <p class="text-default-400 mb-4 text-xs">静态快照 · 来源：refs/proj 12 号金标校准（2026-07-06）。确定性层可靠、LLM 仅建议永不自动接受。</p>
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <div v-for="m in LLM_TRUST_MAP" :key="m.layer" :class="`border-${m.tone}-200`" class="border-default-200 rounded-lg border p-3">
            <p :class="`text-${m.tone}`" class="mb-1 text-sm font-semibold">{{ m.layer }}</p>
            <p class="text-default-500 text-xs">{{ m.note }}</p>
          </div>
        </div>
      </KunCard>
    </template>
  </div>
</template>
