<script setup lang="ts">
// Review queue: lists pending galgame submissions / revisions and lets
// the admin drill into one for approve/decline/ban. Reads from
// GET /admin/galgame/messages. Each message links a galgame + the user
// who triggered the event. Approved/declined messages drop off the queue
// automatically because the JOIN filters galgame.status=3.
import { REVIEW_QUEUE_TABS } from '~/constants/admin'
import type { ReviewMessage, ReviewQueueResponse } from '~/shared/types/review'

const api = useApi()
const activeTab = useQueryState<string>('tab', 'all_pending')
const page = useQueryState('page', 1)
const limit = ref(20)

const items = ref<ReviewMessage[]>([])
const total = ref(0)
const loading = ref(false)

const currentTab = computed(
  () => REVIEW_QUEUE_TABS.find((t) => t.id === activeTab.value) ?? REVIEW_QUEUE_TABS[0]
)

const loadQueue = async () => {
  loading.value = true
  try {
    const response = await api.get<ReviewQueueResponse>(
      '/admin/galgame/messages',
      {
        type: currentTab.value.types,
        page: page.value,
        limit: limit.value
      }
    )
    if (response.code === 0) {
      items.value = response.data.items
      total.value = response.data.total
    }
  } finally {
    loading.value = false
  }
}

watch([activeTab, page, limit], loadQueue, { immediate: true })

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
      <div class="border-default-200 flex rounded-lg border">
        <button
          v-for="tab in REVIEW_QUEUE_TABS"
          :key="tab.id"
          class="flex items-center gap-2 px-4 py-2 text-sm transition-colors first:rounded-l-lg last:rounded-r-lg"
          :class="
            activeTab === tab.id
              ? 'bg-primary text-white'
              : 'text-default-500 hover:bg-default-100'
          "
          @click="switchTab(tab.id)"
        >
          <Icon :name="tab.icon" class="size-4" />
          {{ tab.label }}
        </button>
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
