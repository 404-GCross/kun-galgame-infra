<script setup lang="ts">
import type { CatalogEntitySearch } from '~/shared/types/catalog'
import { ENTITY_SEARCH_TYPES, LABEL_KIND_LABEL } from '~/constants/catalog'

const catalog = useCatalog()

const q = ref('')
const type = ref('names')
const locale = ref('ja')
const result = ref<CatalogEntitySearch | null>(null)
const loading = ref(false)

const run = async () => {
  loading.value = true
  try {
    const r = await catalog.search(q.value, type.value, locale.value, 20)
    result.value = r.code === 0 ? (r.data as CatalogEntitySearch) : null
  } finally {
    loading.value = false
  }
}

// A label hit's id is prefixed b{id}; strip it to link the reverse page.
const labelIdOf = (id: string) => id.replace(/^b/, '')
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-foreground text-2xl font-bold">catalog 实体搜索</h1>
      <NuxtLink to="/catalog-browser">
        <KunButton variant="light" color="default" size="sm">
          <KunIcon name="lucide:arrow-left" class="size-4" />
          仪表盘
        </KunButton>
      </NuxtLink>
    </div>

    <KunCard class="p-5">
      <div class="flex flex-wrap items-center gap-2">
        <KunButton
          v-for="t in ENTITY_SEARCH_TYPES"
          :key="t.value"
          :variant="type === t.value ? 'solid' : 'light'"
          color="primary"
          size="sm"
          @click="type = t.value"
        >
          {{ t.label }}
        </KunButton>
        <span class="text-default-300">|</span>
        <KunButton
          v-for="l in ['ja', 'zh', 'en']"
          :key="l"
          :variant="locale === l ? 'solid' : 'light'"
          color="default"
          size="sm"
          @click="locale = l"
        >
          {{ l }}
        </KunButton>
      </div>
      <div class="mt-3 flex gap-2">
        <input
          v-model="q"
          type="text"
          aria-label="catalog 实体搜索关键词"
          placeholder="名义 / 角色 / 厂牌 名（空查询返回热门）"
          class="border-default-200 bg-default-50 text-foreground focus:border-primary flex-1 rounded-lg border px-3 py-2 text-sm outline-none"
          @keyup.enter="run"
        />
        <KunButton color="primary" size="md" :loading="loading" @click="run">搜索</KunButton>
      </div>
    </KunCard>

    <div v-if="loading" class="text-default-400 flex items-center justify-center py-16">
      <KunIcon name="lucide:loader-circle" class="size-6 animate-spin" />
    </div>

    <template v-else-if="result">
      <p class="text-default-500 text-sm">共 {{ result.total.toLocaleString() }} 命中（展示前 {{ result.items.length }}）</p>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <KunCard v-for="hit in result.items" :key="hit.id" class="p-4">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <p class="text-foreground truncate font-medium">
                <NuxtLink v-if="hit.entity_type === 'label'" class="text-primary" :to="`/catalog-browser/label/${labelIdOf(hit.id)}`">
                  {{ hit.name }}
                </NuxtLink>
                <span v-else>{{ hit.name }}</span>
              </p>
              <p v-if="hit.latin" class="text-default-400 truncate text-xs">{{ hit.latin }}</p>
            </div>
            <span class="text-default-400 shrink-0 text-xs">{{ hit.entity_type }}</span>
          </div>
          <div class="mt-2 flex flex-wrap items-center gap-1">
            <span v-for="src in hit.sources" :key="src" class="bg-default-100 text-default-600 rounded px-1.5 py-0.5 font-mono text-xs">
              {{ src }}
            </span>
            <span v-if="hit.entity_type === 'label' && hit.kind != null" class="text-default-400 text-xs">
              {{ LABEL_KIND_LABEL[hit.kind] ?? hit.kind }}
            </span>
            <span v-if="hit.entity_type === 'credit_name' && hit.person_id == null" class="text-warning text-xs">孤儿</span>
          </div>
        </KunCard>
      </div>
      <p v-if="!result.items.length" class="text-default-400 py-8 text-center">无结果</p>
    </template>

    <p v-else class="text-default-400 py-16 text-center">输入关键词开始搜索</p>
  </div>
</template>
