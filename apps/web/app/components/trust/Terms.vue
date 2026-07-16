<script setup lang="ts">
// Tier0 word-list registry (step 05/06): the deterministic banned/suspect terms
// that back the synchronous /trust/check gate. A term is created (the raw word is
// normalized server-side — we surface the stored normalized form on success),
// filtered by site / kind / deprecation, and deprecated (never hard-deleted, and
// never un-deprecated — deprecation is terminal). Managing terms requires
// trust.term_manage (admin/主理人); a moderator gets a 403 the same way the other
// trust panels surface a permission failure.
import type {
  TrustTerm,
  TrustTermsResponse,
  TrustCreateTermRequest
} from '~~/shared/types/trust'
import {
  TERM_KIND,
  TERM_KIND_LABELS,
  TERM_KIND_COLORS,
  TRUST_FILTER_ALL
} from '~/constants/trust'

const api = useApi('trust')

// Filters
const site = ref('')
const kind = ref<number>(TRUST_FILTER_ALL)
const includeDeprecated = ref(false)

const { data, refresh } = await useApiFetch<TrustTermsResponse>(
  '/admin/trust/terms',
  {
    query: computed(() => ({
      site: site.value || undefined,
      kind: kind.value,
      include_deprecated: includeDeprecated.value || undefined
    }))
  },
  'trust'
)
const terms = computed<TrustTerm[]>(() => data.value?.terms ?? [])

const kindFilterOptions = [
  { value: TRUST_FILTER_ALL, label: '全部类型' },
  { value: TERM_KIND.suspect, label: TERM_KIND_LABELS[TERM_KIND.suspect]! },
  { value: TERM_KIND.banned, label: TERM_KIND_LABELS[TERM_KIND.banned]! }
]
const kindCreateOptions = [
  { value: TERM_KIND.suspect, label: TERM_KIND_LABELS[TERM_KIND.suspect]! },
  { value: TERM_KIND.banned, label: TERM_KIND_LABELS[TERM_KIND.banned]! }
]

// Create
const createOpen = ref(false)
const form = reactive({
  term: '',
  kind: TERM_KIND.suspect as number,
  site: '',
  note: ''
})
const creating = ref(false)

const openCreate = () => {
  form.term = ''
  form.kind = TERM_KIND.suspect
  form.site = site.value || ''
  form.note = ''
  createOpen.value = true
}

const create = async () => {
  if (!form.term.trim()) {
    useKunMessage('词条必填', 'warn')
    return
  }
  creating.value = true
  try {
    const body: TrustCreateTermRequest = {
      term: form.term.trim(),
      kind: form.kind
    }
    if (form.site.trim()) body.site = form.site.trim()
    if (form.note.trim()) body.note = form.note.trim()
    const res = await api.post<TrustTerm>('/admin/trust/terms', body)
    if (res.code === 0) {
      // Surface the server-normalized form so the operator sees exactly what was
      // stored (case/width/space folding happens server-side).
      useKunMessage(`已创建,归一化为「${res.data.term_norm}」`, 'success')
      createOpen.value = false
      await refresh()
    } else {
      useKunMessage(res.message || '创建失败', 'error')
    }
  } finally {
    creating.value = false
  }
}

// Deprecate (with confirm; terminal — no un-deprecate)
const depOpen = ref(false)
const depTarget = ref<TrustTerm | null>(null)
const deprecating = ref(false)

const askDeprecate = (t: TrustTerm) => {
  depTarget.value = t
  depOpen.value = true
}

const confirmDeprecate = async () => {
  if (!depTarget.value) return
  deprecating.value = true
  try {
    const res = await api.post<TrustTerm>(
      `/admin/trust/terms/${depTarget.value.id}/deprecate`
    )
    if (res.code === 0) {
      useKunMessage('已弃用', 'success')
      depOpen.value = false
      await refresh()
    } else {
      useKunMessage(res.message || '弃用失败', 'error')
    }
  } finally {
    deprecating.value = false
  }
}
</script>

<template>
  <div class="space-y-4">
    <h1 class="text-foreground text-2xl font-bold">T&S 审核队列</h1>
    <TrustSubNav />
    <KunCard content-class="space-y-3 p-4">
      <div class="flex flex-wrap items-center gap-2">
        <h2 class="text-foreground text-lg font-bold">Tier0 词表</h2>
        <KunInput v-model="site" placeholder="按站点过滤" class="w-36" />
        <KunSelect v-model="kind" :options="kindFilterOptions" class="w-40" />
        <KunSwitch v-model="includeDeprecated" label="含已弃用" />
        <KunButton
          color="primary"
          size="sm"
          class="ml-auto"
          @click="openCreate"
        >
          <KunIcon name="lucide:plus" class="mr-1 size-4" />
          新建
        </KunButton>
      </div>

      <div class="overflow-x-auto">
        <table class="w-full min-w-[40rem] text-sm">
          <thead class="text-default-500">
            <tr>
              <th class="px-2 py-2 text-left font-medium">词条(归一化)</th>
              <th class="px-2 py-2 text-left font-medium">类型</th>
              <th class="px-2 py-2 text-left font-medium">归属</th>
              <th class="px-2 py-2 text-left font-medium">备注</th>
              <th class="px-2 py-2 text-left font-medium">状态</th>
              <th class="px-2 py-2 text-right font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="t in terms"
              :key="t.id"
              class="border-default-200 border-t align-top"
              :class="t.is_deprecated ? 'opacity-60' : ''"
            >
              <td class="px-2 py-2 font-mono break-all">{{ t.term_norm }}</td>
              <td class="px-2 py-2">
                <KunChip
                  :color="TERM_KIND_COLORS[t.kind]"
                  variant="flat"
                  size="xs"
                >
                  {{ TERM_KIND_LABELS[t.kind] || t.kind }}
                </KunChip>
              </td>
              <td class="px-2 py-2">
                <KunChip
                  :color="t.site ? 'secondary' : 'info'"
                  variant="flat"
                  size="xs"
                >
                  {{ t.site || '全局' }}
                </KunChip>
              </td>
              <td class="text-default-400 max-w-[16rem] truncate px-2 py-2">
                {{ t.note || '—' }}
              </td>
              <td class="px-2 py-2">
                <KunChip
                  :color="t.is_deprecated ? 'default' : 'success'"
                  variant="flat"
                  size="xs"
                >
                  {{ t.is_deprecated ? '已弃用' : '启用中' }}
                </KunChip>
              </td>
              <td class="px-2 py-2 text-right">
                <KunButton
                  v-if="!t.is_deprecated"
                  color="warning"
                  variant="flat"
                  size="sm"
                  @click="askDeprecate(t)"
                >
                  弃用
                </KunButton>
                <span v-else class="text-default-300">—</span>
              </td>
            </tr>
            <tr v-if="!terms.length">
              <td colspan="6" class="text-default-400 px-2 py-8 text-center">
                暂无词条
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <KunModal v-model="createOpen">
        <div class="space-y-4">
          <h2 class="text-foreground text-xl font-bold">新建 Tier0 词条</h2>
          <p class="text-default-500 text-sm">
            提交原词,系统会做大小写 / 全半角 /
            空白归一化后存储;创建成功后会显示归一化结果。
          </p>
          <KunInput
            v-model="form.term"
            label="词条"
            placeholder="原词,如 spam / 广告"
          />
          <KunSelect
            v-model="form.kind"
            label="类型"
            :options="kindCreateOptions"
          />
          <KunInput
            v-model="form.site"
            label="站点(可选,留空为全局)"
            placeholder="如 kungal"
          />
          <KunInput
            v-model="form.note"
            label="备注(可选)"
            placeholder="运营备注"
          />
          <div class="flex justify-end gap-3">
            <KunButton
              color="default"
              variant="flat"
              @click="createOpen = false"
            >
              取消
            </KunButton>
            <KunButton color="primary" :loading="creating" @click="create">
              创建
            </KunButton>
          </div>
        </div>
      </KunModal>

      <KunModal v-model="depOpen">
        <div class="space-y-4">
          <h2 class="text-foreground text-xl font-bold">弃用词条</h2>
          <p class="text-default-500 text-sm">
            确认弃用
            <span class="text-foreground font-mono font-semibold">{{
              depTarget?.term_norm
            }}</span>
            ?弃用后该词条立即停止生效,且无法恢复(如需再次启用请重新创建)。
          </p>
          <div class="flex justify-end gap-3">
            <KunButton color="default" variant="flat" @click="depOpen = false">
              取消
            </KunButton>
            <KunButton
              color="warning"
              :loading="deprecating"
              @click="confirmDeprecate"
            >
              确认弃用
            </KunButton>
          </div>
        </div>
      </KunModal>
    </KunCard>
  </div>
</template>
