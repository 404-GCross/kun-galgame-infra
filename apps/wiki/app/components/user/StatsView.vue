<script setup lang="ts">
import type { UserGalgameStats } from '~/shared/types/user-stats'

const api = useApi()
const route = useRoute()
const router = useRouter()

const id = computed(() => Number(route.params.id))

// SSR: public endpoint, server-fetched + payload-hydrated; re-fetches on
// id change.
const { data: stats, status } = await useAsyncData(
  'user-galgame-stats',
  async () => {
    const r = await api.get<UserGalgameStats>(
      `/galgame/user/${id.value}/stats`
    )
    return r.code === 0 ? r.data : null
  },
  { watch: [id] }
)
const loading = computed(() => status.value === 'pending')

const cards = computed(() => {
  const s = stats.value
  return [
    {
      label: '创建的 galgame',
      value: s?.galgame_created ?? 0,
      icon: 'lucide:plus-circle',
      color: 'text-primary'
    },
    {
      label: '今日新建',
      value: s?.galgame_created_today ?? 0,
      icon: 'lucide:sunrise',
      color: 'text-warning'
    },
    {
      label: '参与贡献',
      value: s?.galgame_contributed ?? 0,
      icon: 'lucide:users',
      color: 'text-info'
    },
    {
      label: 'Revision 次数',
      value: s?.revision_count ?? 0,
      icon: 'lucide:history',
      color: 'text-default-500'
    }
  ]
})

const prStats = computed(() => {
  const s = stats.value
  return [
    {
      label: '已提交',
      value: s?.pr_submitted ?? 0,
      color: 'text-default-500'
    },
    { label: '已合并', value: s?.pr_merged ?? 0, color: 'text-success' },
    { label: '已拒绝', value: s?.pr_declined ?? 0, color: 'text-danger' },
    { label: '待审核', value: s?.pr_pending ?? 0, color: 'text-warning' }
  ]
})
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
      <h1 class="text-foreground text-2xl font-bold">
        用户贡献统计 · id={{ id }}
      </h1>
    </div>

    <div
      v-if="loading && !stats"
      class="text-default-400 flex items-center justify-center py-20"
    >
      <Icon name="lucide:loader-2" class="size-6 animate-spin" />
    </div>

    <template v-else-if="stats">
      <div class="grid grid-cols-2 gap-4 md:grid-cols-4">
        <KunCard v-for="c in cards" :key="c.label" class="p-4">
          <div class="flex items-center gap-3">
            <div class="bg-default-100 rounded-lg p-2" :class="c.color">
              <Icon :name="c.icon" class="size-6" />
            </div>
            <div class="min-w-0">
              <p class="text-default-500 truncate text-xs">{{ c.label }}</p>
              <p class="text-foreground text-xl font-bold">
                {{ c.value.toLocaleString() }}
              </p>
            </div>
          </div>
        </KunCard>
      </div>

      <KunCard class="p-6">
        <h2 class="text-foreground mb-3 text-base font-semibold">PR 贡献</h2>
        <div class="grid grid-cols-2 gap-4 md:grid-cols-4">
          <div
            v-for="p in prStats"
            :key="p.label"
            class="border-default-200 rounded-lg border p-3"
          >
            <p class="text-default-500 text-xs">{{ p.label }}</p>
            <p class="mt-1 text-2xl font-bold" :class="p.color">
              {{ p.value.toLocaleString() }}
            </p>
          </div>
        </div>
      </KunCard>
    </template>
  </div>
</template>
