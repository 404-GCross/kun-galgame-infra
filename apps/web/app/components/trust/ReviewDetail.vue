<script setup lang="ts">
// Review-item detail shown inside the queue's modal: the item summary, its
// linked reports (reason mapped from the taxonomy, note, weight, reporter,
// time), and the decide form. A decision is either `dismissed` (no action) or
// `actioned` — the latter carries an enforcement action, a reason_code drawn
// from the reason taxonomy, and an optional Art.17 statement, and writes a
// disposition server-side.
import {
  REVIEW_STATUS,
  REVIEW_STATUS_LABELS,
  REVIEW_STATUS_COLORS,
  REVIEW_SOURCE_LABELS,
  REPORT_STATUS_LABELS,
  REPORT_STATUS_COLORS,
  ACTION_LABELS,
  DECIDE_ACTIONS
} from '~/constants/trust'
import type {
  TrustReviewItemDetail,
  TrustReason,
  TrustDecideData
} from '~~/shared/types/trust'

const props = defineProps<{
  detail: TrustReviewItemDetail
  reasons: TrustReason[]
}>()
const emit = defineEmits<{ decided: [] }>()

const api = useApi('trust')

const item = computed(() => props.detail.item)
const reports = computed(() => props.detail.reports ?? [])

// A decision is only allowed while the item is pending or claimed.
const canDecide = computed(
  () =>
    item.value.status === REVIEW_STATUS.pending ||
    item.value.status === REVIEW_STATUS.claimed
)

// reason_id -> label, for rendering each report's reason.
const reasonById = computed<Record<number, TrustReason>>(() => {
  const map: Record<number, TrustReason> = {}
  for (const r of props.reasons) map[r.id] = r
  return map
})
const reasonLabel = (id: number) => reasonById.value[id]?.name_cn ?? `#${id}`

// The reason_code options for the decide form: non-deprecated reasons that are
// global (no site) or belong to this item's site.
const reasonOptions = computed(() =>
  props.reasons.filter(
    (r) => !r.is_deprecated && (!r.site || r.site === item.value.site)
  )
)

const decision = ref<'dismissed' | 'actioned'>('actioned')
const action = ref(0)
const reasonCode = ref('')
const statement = ref('')
const submitting = ref(false)

const submit = async () => {
  if (decision.value === 'actioned') {
    if (!action.value) {
      useKunMessage('请选择处置动作', 'warn')
      return
    }
    if (!reasonCode.value) {
      useKunMessage('请选择理由分类', 'warn')
      return
    }
  }
  submitting.value = true
  try {
    const body: Record<string, unknown> = { decision: decision.value }
    if (decision.value === 'actioned') {
      body.action = action.value
      body.reason_code = reasonCode.value
      if (statement.value.trim()) body.statement = statement.value.trim()
    }
    const res = await api.post<TrustDecideData>(
      `/admin/trust/review-items/${item.value.id}/decide`,
      body
    )
    if (res.code === 0) {
      useKunMessage(
        decision.value === 'actioned'
          ? `已处置${
              res.data?.disposition_id
                ? `(处置 #${res.data.disposition_id})`
                : ''
            }`
          : '已驳回',
        'success'
      )
      emit('decided')
    } else {
      // 409 = illegal state transition; surface the server message verbatim.
      useKunMessage(res.message || '裁决失败', 'error')
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center gap-2">
      <h2 class="text-foreground text-xl font-bold">审核详情</h2>
      <KunChip color="info" variant="flat" size="xs">
        {{ item.subject_kind }}
      </KunChip>
      <span class="font-mono text-sm">{{ item.subject_id }}</span>
      <KunChip
        :color="REVIEW_STATUS_COLORS[item.status] ?? 'default'"
        variant="flat"
        size="xs"
        class="ml-auto"
      >
        {{ REVIEW_STATUS_LABELS[item.status] ?? item.status }}
      </KunChip>
    </div>

    <div class="text-default-500 grid grid-cols-2 gap-2 text-sm sm:grid-cols-3">
      <span>站点:{{ item.site }}</span>
      <span>来源:{{ REVIEW_SOURCE_LABELS[item.source] ?? item.source }}</span>
      <span>优先级:{{ item.priority?.toFixed(2) }}</span>
      <span>严重度:{{ item.severity ?? '-' }}</span>
      <span>权重合计:{{ item.report_weight_sum?.toFixed(1) ?? '-' }}</span>
      <span v-if="item.claimed_by">认领人:{{ item.claimed_by }}</span>
    </div>

    <div class="space-y-2">
      <p class="text-foreground text-sm font-medium">
        关联举报({{ reports.length }})
      </p>
      <div
        v-for="r in reports"
        :key="r.id"
        class="border-default-200 space-y-1 rounded-lg border p-3"
      >
        <div class="flex flex-wrap items-center gap-2 text-sm">
          <KunChip color="default" variant="flat" size="xs">
            {{ reasonLabel(r.reason_id) }}
          </KunChip>
          <KunChip
            :color="REPORT_STATUS_COLORS[r.status] ?? 'default'"
            variant="flat"
            size="xs"
          >
            {{ REPORT_STATUS_LABELS[r.status] ?? r.status }}
          </KunChip>
          <span class="text-default-400">举报人 {{ r.reporter_id }}</span>
          <span class="text-default-400">权重 {{ r.weight?.toFixed(1) }}</span>
          <span class="text-default-400 ml-auto">
            {{ new Date(r.created_at).toLocaleString() }}
          </span>
        </div>
        <p v-if="r.note" class="text-default-500 text-sm whitespace-pre-line">
          {{ r.note }}
        </p>
        <div
          v-if="r.subject_snapshot"
          class="bg-content2 rounded-lg p-2 text-sm"
        >
          <p class="text-default-400 mb-1 text-xs">举报时点内容快照</p>
          <p class="text-default-500 break-words whitespace-pre-line">
            {{ r.subject_snapshot }}
          </p>
        </div>
      </div>
      <p v-if="!reports.length" class="text-default-400 text-sm">
        该审核项无关联举报(可能来自非举报来源)。
      </p>
    </div>

    <template v-if="canDecide">
      <KunDivider />
      <div class="space-y-3">
        <p class="text-foreground text-sm font-medium">裁决</p>
        <div class="flex flex-wrap gap-2">
          <KunButton
            :color="decision === 'actioned' ? 'primary' : 'default'"
            :variant="decision === 'actioned' ? 'solid' : 'flat'"
            size="sm"
            @click="decision = 'actioned'"
          >
            处置
          </KunButton>
          <KunButton
            :color="decision === 'dismissed' ? 'primary' : 'default'"
            :variant="decision === 'dismissed' ? 'solid' : 'flat'"
            size="sm"
            @click="decision = 'dismissed'"
          >
            驳回
          </KunButton>
        </div>

        <template v-if="decision === 'actioned'">
          <div class="flex flex-wrap items-center gap-2">
            <select
              v-model.number="action"
              aria-label="处置动作"
              class="border-default-200 bg-background text-foreground rounded-lg border px-2 py-1.5 text-sm"
            >
              <option :value="0" disabled>选择处置动作</option>
              <option v-for="a in DECIDE_ACTIONS" :key="a" :value="a">
                {{ ACTION_LABELS[a] }}
              </option>
            </select>
            <select
              v-model="reasonCode"
              aria-label="理由分类"
              class="border-default-200 bg-background text-foreground rounded-lg border px-2 py-1.5 text-sm"
            >
              <option value="" disabled>选择理由分类</option>
              <option v-for="r in reasonOptions" :key="r.id" :value="r.key">
                {{ r.name_cn }}
              </option>
            </select>
          </div>
          <KunTextarea
            v-model="statement"
            :rows="3"
            placeholder="理由声明(可选,Art.17)"
          />
        </template>

        <div class="flex justify-end">
          <KunButton
            :color="decision === 'actioned' ? 'success' : 'warning'"
            :loading="submitting"
            @click="submit"
          >
            {{ decision === 'actioned' ? '确认处置' : '确认驳回' }}
          </KunButton>
        </div>
      </div>
    </template>

    <div v-else class="text-default-400 text-sm">
      该审核项已决策({{ REVIEW_STATUS_LABELS[item.status] }}
      <template v-if="item.decided_by">· 操作者 {{ item.decided_by }}</template>
      <template v-if="item.decided_at">
        · {{ new Date(item.decided_at).toLocaleString() }}</template
      >)。
    </div>
  </div>
</template>
