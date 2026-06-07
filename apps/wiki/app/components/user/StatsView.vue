<script setup lang="ts">
import type { UserGalgameStats } from '~/shared/types/user-stats'

const api = useApi()
const route = useRoute()
const router = useRouter()
const { isAdmin } = useAuth()

const id = computed(() => Number(route.params.id))

// SSR: public endpoint, server-fetched + payload-hydrated; re-fetches on
// id change.
const { data: stats, status, refresh } = await useAsyncData(
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

// Danger zone (admin only): bulk soft-delete this user's galgame. The
// content-side companion to the OAuth anonymize action for severe spam —
// flips every published/pending galgame to status=banned (hidden from
// lists + search) while keeping rows + revision history, so a mis-flag is
// recoverable via the normal status path. Account ban/anonymize is a
// separate action in the OAuth user-management admin.
const banning = ref(false)
const banAllContent = async () => {
  const ok = await useKunConfirm({
    title: '封禁该用户的全部 Galgame',
    content:
      '将该用户创建的所有「已发布 / 待审」galgame 软删（状态置为封禁，从列表与搜索中隐藏，但保留记录与修订历史，可单独恢复）。用于严重 spam 清理。确认继续？',
    confirmText: '确认封禁',
    danger: true
  })
  if (!ok) return

  banning.value = true
  try {
    const r = await api.post<{ banned_count: number; ids: number[] }>(
      `/admin/galgame/ban-by-user/${id.value}`
    )
    if (r.code === 0) {
      useKunMessage(`已封禁 ${r.data.banned_count} 个 galgame`, 'success')
      await refresh()
    } else {
      useKunMessage('操作失败，请稍后重试', 'error')
    }
  } finally {
    banning.value = false
  }
}

const cards = computed(() => {
  const s = stats.value
  return [
    {
      label: '创建的 galgame',
      value: s?.galgame_created ?? 0,
      icon: 'lucide:circle-plus',
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
      <KunButton
        variant="light"
        size="sm"
        is-icon-only
        aria-label="返回"
        @click="router.back()"
      >
        <KunIcon name="lucide:arrow-left" class="size-5" />
      </KunButton>
      <h1 class="text-foreground text-2xl font-bold">
        用户贡献统计 · id={{ id }}
      </h1>
    </div>

    <div
      v-if="loading && !stats"
      class="text-default-400 flex items-center justify-center py-20"
    >
      <KunIcon name="lucide:loader-circle" class="size-6 animate-spin" />
    </div>

    <template v-else-if="stats">
      <div class="grid grid-cols-2 gap-4 md:grid-cols-4">
        <KunCard v-for="c in cards" :key="c.label" class="p-4">
          <div class="flex items-center gap-3">
            <div class="bg-default-100 rounded-lg p-2" :class="c.color">
              <KunIcon :name="c.icon" class="size-6" />
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

      <!-- Danger zone: bulk content cleanup for severe spam (admin only).
           Account-level ban/anonymize lives in the OAuth user admin. -->
      <KunCard v-if="isAdmin" class-name="border-danger-200" class="p-6">
        <h2 class="text-danger mb-1 text-base font-semibold">危险操作</h2>
        <p class="text-default-500 mb-3 text-sm">
          软删该用户创建的全部 galgame（隐藏但保留记录，可恢复）。账号本身的封禁 / 注销匿名化请在 OAuth 用户管理端处理。
        </p>
        <KunButton
          color="danger"
          variant="flat"
          :loading="banning"
          :disabled="banning"
          @click="banAllContent"
        >
          <KunIcon name="lucide:trash-2" class="mr-1 size-4" />
          封禁该用户全部内容
        </KunButton>
      </KunCard>
    </template>
  </div>
</template>
