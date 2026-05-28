<script setup lang="ts">
import { DASHBOARD_STAT_CARDS } from '~/constants/dashboard'

const auth = useAuth()
const api = useApi()

// Live counts, drawn from the admin/list endpoints the dashboard user
// already has access to (page is guarded by middleware ['auth','admin']):
//   users   → GET /admin/users (paginated; read `total`)
//   sites   → GET /sites       (full array; length)
//   clients → GET /oauth/clients (full array; length)
const counts = reactive<Record<string, number>>({
  users: 0,
  sites: 0,
  clients: 0,
})
const loading = ref(true)

const loadCounts = async () => {
  loading.value = true
  try {
    const [usersRes, sitesRes, clientsRes] = await Promise.all([
      api.get<{ total: number }>('/admin/users?page=1&limit=1'),
      api.get<Site[]>('/sites'),
      api.get<OAuthClient[]>('/oauth/clients'),
    ])
    if (usersRes.code === 0) counts.users = usersRes.data?.total ?? 0
    if (sitesRes.code === 0) counts.sites = sitesRes.data?.length ?? 0
    if (clientsRes.code === 0) counts.clients = clientsRes.data?.length ?? 0
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  if (!auth.user.value) await auth.fetchUser()
  await loadCounts()
})

const stats = computed(() =>
  DASHBOARD_STAT_CARDS.map((c) => ({
    label: c.label,
    icon: c.icon,
    color: c.color,
    value: loading.value ? '—' : String(counts[c.key] ?? 0),
  }))
)
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-bold text-foreground">仪表盘</h1>
      <p class="mt-1 text-default-500">
        欢迎回来，{{ auth.user.value?.name }}
      </p>
    </div>

    <div class="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <DashboardStatCard
        v-for="stat in stats"
        :key="stat.label"
        v-bind="stat"
      />
    </div>

    <DashboardQuickActions />

    <KunCard :bordered="false" content-class="justify-start gap-0" class-name="rounded-xl bg-content1 p-6 shadow-sm">
      <h2 class="mb-4 text-lg font-semibold text-foreground">
        最近活动
      </h2>
      <div class="py-8 text-center text-default-400">
        <Icon name="lucide:inbox" class="mx-auto mb-2 size-12 opacity-50" />
        <p>暂无活动记录</p>
      </div>
    </KunCard>
  </div>
</template>
