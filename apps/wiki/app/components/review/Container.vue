<script setup lang="ts">
// Review queue: lists pending galgame submissions / revisions and lets
// the admin drill into one for approve/decline/ban. Reads from
// GET /admin/galgame/messages. Each message links a galgame + the user
// who triggered the event. Approved/declined messages drop off the queue
// automatically because the JOIN filters galgame.status=3.
import { REVIEW_QUEUE_TABS } from '~/constants/admin'
import type { ReviewQueueResponse } from '~/shared/types/review'

const api = useApi()
const activeTab = useQueryState<string>('tab', 'all_pending')
const page = useQueryState('page', 1)
const limit = ref(20)

const currentTab = computed(
  () => REVIEW_QUEUE_TABS.find((t) => t.id === activeTab.value) ?? REVIEW_QUEUE_TABS[0]
)

// SSR: server-fetched + payload-hydrated, matching the rest of the wiki
// (galgame/List et al). null = fetch failed (e.g. SSR-without-token); a
// valid empty result is { items: [], total: 0 } so the two are distinct.
const {
  data,
  status: fetchStatus,
  refresh
} = await useAsyncData(
  'review-queue',
  async () => {
    const r = await api.get<ReviewQueueResponse>('/admin/galgame/messages', {
      type: currentTab.value.types,
      page: page.value,
      limit: limit.value
    })
    return r.code === 0 ? r.data : null
  },
  { watch: [activeTab, page, limit] }
)

const items = computed(() => data.value?.items ?? [])
const total = computed(() => data.value?.total ?? 0)
const loading = computed(() => fetchStatus.value === 'pending')

// Self-heal the SSR-without-token edge: only when the fetch actually
// failed (data null), not for a legitimately empty result set.
onMounted(() => {
  if (!data.value) refresh()
})

const switchTab = (id: string) => {
  activeTab.value = id
  page.value = 1
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-foreground text-2xl font-bold">审核队列</h1>
      <span class="text-default-500 text-sm">共 {{ total }} 条</span>
    </div>

    <div class="flex flex-wrap items-center gap-3">
      <div class="flex gap-2">
        <KunButton
          v-for="tab in REVIEW_QUEUE_TABS"
          :key="tab.id"
          :variant="activeTab === tab.id ? 'solid' : 'light'"
          color="primary"
          size="sm"
          @click="switchTab(tab.id)"
        >
          <Icon :name="tab.icon" class="mr-1 size-4" />
          {{ tab.label }}
        </KunButton>
      </div>
    </div>

    <ReviewQueueTable :items="items" :loading="loading" />

    <KunPagination
      v-if="total > limit"
      v-model:current-page="page"
      :total-page="Math.ceil(total / limit)"
    />
  </div>
</template>
