<script setup lang="ts">
// The T&S review inbox (doc 18 §5.2). Lists review items ordered by the
// backend (priority DESC), filterable by site / status / source. Row actions:
// claim a pending item (FOR UPDATE SKIP LOCKED server-side; a 409 means
// someone else took it — surfaced as a friendly message), or open the detail
// modal to inspect the linked reports and decide.
import {
  TRUST_FILTER_ALL,
  REVIEW_STATUS,
  REVIEW_STATUS_LABELS,
  REVIEW_STATUS_COLORS,
  REVIEW_SOURCE_LABELS
} from '~/constants/trust'
import type {
  TrustReviewItem,
  TrustReviewItemPage,
  TrustReviewItemDetail,
  TrustReason
} from '~~/shared/types/trust'

const api = useApi('trust')

const page = ref(1)
const limit = 50
const site = ref('')
const status = ref(TRUST_FILTER_ALL)
const source = ref(TRUST_FILTER_ALL)
watch([site, status, source], () => {
  page.value = 1
})

const { data, status: fetchStatus, refresh, error } =
  await useApiFetch<TrustReviewItemPage>(
    '/admin/trust/review-items',
    {
      query: computed(() => ({
        page: page.value,
        limit,
        site: site.value || undefined,
        status: status.value,
        source: source.value
      }))
    },
    'trust'
  )
const items = computed(() => data.value?.items ?? [])
const total = computed(() => data.value?.total ?? 0)
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / limit)))
const isLoading = computed(() => fetchStatus.value === 'pending')

// The reason taxonomy (global base + every site's extensions), fetched once —
// the detail modal maps a report's reason_id to its label and feeds the
// decide form's reason_code select.
const { data: reasonsData } = await useApiFetch<TrustReason[]>(
  '/admin/trust/report-reasons',
  {},
  'trust'
)
const reasons = computed<TrustReason[]>(() => reasonsData.value ?? [])

const statusOptions = computed(() => [
  { value: TRUST_FILTER_ALL, label: '全部状态' },
  ...Object.entries(REVIEW_STATUS_LABELS).map(([id, label]) => ({
    value: Number(id),
    label
  }))
])
const sourceOptions = computed(() => [
  { value: TRUST_FILTER_ALL, label: '全部来源' },
  ...Object.entries(REVIEW_SOURCE_LABELS).map(([id, label]) => ({
    value: Number(id),
    label
  }))
])

// The classifier score reads on the same 0-1 scale the AI gateway emits. 0.4 is
// where the cascade escalates to the LLM tier, so anything at or above it is a
// verdict a second model already looked at; the bands here mirror that.
const scoreColor = (score: number) => {
  if (score >= 0.8) return 'danger'
  if (score >= 0.4) return 'warning'
  return 'default'
}

const claiming = ref(0)
const claim = async (item: TrustReviewItem) => {
  claiming.value = item.id
  try {
    const res = await api.post(`/admin/trust/review-items/${item.id}/claim`)
    if (res.code === 0) {
      useKunMessage('已认领', 'success')
      await refresh()
    } else {
      // 409: another reviewer already claimed it. Surface the server message
      // plus a friendly hint.
      useKunMessage(res.message || '认领失败（可能已被他人认领）', 'warn')
      await refresh()
    }
  } finally {
    claiming.value = 0
  }
}

const detailOpen = ref(false)
const detail = ref<TrustReviewItemDetail | null>(null)
const loadingDetail = ref(false)

const openDetail = async (item: TrustReviewItem) => {
  loadingDetail.value = true
  detail.value = null
  detailOpen.value = true
  try {
    const res = await api.get<TrustReviewItemDetail>(
      `/admin/trust/review-items/${item.id}`
    )
    if (res.code === 0) {
      detail.value = res.data
    } else {
      useKunMessage(res.message || '加载详情失败', 'error')
      detailOpen.value = false
    }
  } finally {
    loadingDetail.value = false
  }
}

const onDecided = async () => {
  detailOpen.value = false
  await refresh()
}
</script>

<template>
  <div class="space-y-4">
    <h1 class="text-foreground text-2xl font-bold">T&S 审核队列</h1>
    <TrustSubNav />

    <div class="flex flex-wrap items-center gap-2">
      <KunInput
        v-model="site"
        placeholder="按站点过滤（留空为全部）"
        class="w-56"
      />
      <KunSelect
        v-model="status"
        :options="statusOptions"
        aria-label="状态过滤"
        class="w-40"
      />
      <KunSelect
        v-model="source"
        :options="sourceOptions"
        aria-label="来源过滤"
        class="w-40"
      />
    </div>
    <p class="text-default-500 text-sm">共 {{ total }} 条(按优先级降序)</p>

    <CommonFetchError v-if="error" @retry="refresh" />

    <div class="bg-content1 overflow-x-auto rounded-xl shadow-sm">
      <table class="w-full min-w-[64rem] text-sm">
        <thead class="bg-content2 text-default-500">
          <tr>
            <th class="px-3 py-2 text-left font-medium">站点</th>
            <th class="px-3 py-2 text-left font-medium">主体</th>
            <th class="px-3 py-2 text-left font-medium">来源</th>
            <th class="px-3 py-2 text-right font-medium">AI 分</th>
            <th class="px-3 py-2 text-right font-medium">严重度</th>
            <th class="px-3 py-2 text-right font-medium">权重</th>
            <th class="px-3 py-2 text-left font-medium">状态</th>
            <th class="px-3 py-2 text-left font-medium">时间</th>
            <th class="px-3 py-2 text-right font-medium">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="item in items"
            :key="item.id"
            class="border-default-200 border-t align-top"
          >
            <td class="px-3 py-2">{{ item.site }}</td>
            <td class="px-3 py-2">
              <KunChip color="info" variant="flat" size="xs" class="mr-1">
                {{ item.subject_kind }}
              </KunChip>
              <span class="font-mono">{{ item.subject_id }}</span>
            </td>
            <td class="px-3 py-2">
              {{ REVIEW_SOURCE_LABELS[item.source] ?? item.source }}
            </td>
            <td class="px-3 py-2 text-right">
              <!-- The classifier verdict that opened (or was merged into) this
                   item. Only AI sources carry one, so a report-only item shows
                   a dash rather than a misleading 0.00. -->
              <KunChip
                v-if="item.classifier_score != null"
                :color="scoreColor(item.classifier_score)"
                variant="flat"
                size="xs"
              >
                {{ item.classifier_score.toFixed(2) }}
              </KunChip>
              <span v-else class="text-default-300">-</span>
            </td>
            <td class="px-3 py-2 text-right">{{ item.severity ?? '-' }}</td>
            <td class="px-3 py-2 text-right">
              {{ item.report_weight_sum?.toFixed(1) ?? '-' }}
            </td>
            <td class="px-3 py-2">
              <KunChip
                :color="REVIEW_STATUS_COLORS[item.status] ?? 'default'"
                variant="flat"
                size="xs"
              >
                {{ REVIEW_STATUS_LABELS[item.status] ?? item.status }}
              </KunChip>
            </td>
            <td class="text-default-400 px-3 py-2">
              {{ new Date(item.created_at).toLocaleString() }}
            </td>
            <td class="px-3 py-2 text-right">
              <div class="flex justify-end gap-2">
                <KunButton
                  v-if="item.status === REVIEW_STATUS.pending"
                  color="primary"
                  variant="flat"
                  size="sm"
                  :loading="claiming === item.id"
                  @click="claim(item)"
                >
                  认领
                </KunButton>
                <KunButton
                  color="default"
                  variant="flat"
                  size="sm"
                  @click="openDetail(item)"
                >
                  查看
                </KunButton>
              </div>
            </td>
          </tr>
          <tr v-if="!items.length && !error">
            <td colspan="9" class="text-default-400 px-3 py-10 text-center">
              {{ isLoading ? '加载中…' : '没有匹配的审核项' }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <KunPagination
      v-model:current-page="page"
      :total-page="totalPages"
      :is-loading="isLoading"
    />

    <KunModal v-model="detailOpen" size="lg">
      <div v-if="loadingDetail" class="flex justify-center py-10">
        <KunIcon
          name="lucide:loader-circle"
          class="text-primary size-8 animate-spin"
        />
      </div>
      <TrustReviewDetail
        v-else-if="detail"
        :detail="detail"
        :reasons="reasons"
        @decided="onDecided"
      />
    </KunModal>
  </div>
</template>
