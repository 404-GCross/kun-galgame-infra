<script setup lang="ts">
import { DASHBOARD_STAT_CARDS } from '~/constants/dashboard'

// Greeting only — user is populated client-side by the layout (auth.user is
// not SSR'd; see profile/auth note).
const auth = useAuth()

// SSR-rendered (kungal-style): the three counts are fetched on the server so
// the dashboard cards paint with numbers on first load. Endpoints the admin
// already has access to (page guarded by middleware ['auth','admin']):
//   users   → GET /admin/users (paginated; read `total`)
//   sites   → GET /sites       (full array; length)
//   clients → GET /oauth/clients (full array; length)
const { data: usersData } = await useApiFetch<{ total: number }>(
  '/admin/users',
  { query: { page: 1, limit: 1 } }
)
const { data: sitesData } = await useApiFetch<Site[]>('/sites')
const { data: clientsData } = await useApiFetch<OAuthClient[]>('/oauth/clients')

const counts = computed<Record<string, number>>(() => ({
  users: usersData.value?.total ?? 0,
  sites: sitesData.value?.length ?? 0,
  clients: clientsData.value?.length ?? 0,
}))

const stats = computed(() =>
  DASHBOARD_STAT_CARDS.map((c) => ({
    label: c.label,
    icon: c.icon,
    color: c.color,
    value: String(counts.value[c.key] ?? 0),
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

    <KunCard content-class="justify-start gap-0" class-name="p-6">
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
