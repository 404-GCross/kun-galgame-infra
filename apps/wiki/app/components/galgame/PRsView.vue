<script setup lang="ts">
import type { GalgamePR } from '~/shared/types/galgame'

const api = useApi()
const route = useRoute()
const router = useRouter()

const galgameId = computed(() => Number(route.params.id))

const page = useQueryState('page', 1)
const limit = ref(20)

const items = ref<GalgamePR[]>([])
const total = ref(0)
const loading = ref(false)

const diffOpen = ref(false)
const diffPR = ref<GalgamePR | null>(null)

const load = async () => {
  loading.value = true
  try {
    const response = await api.get<{ items: GalgamePR[]; total: number }>(
      `/galgame/${galgameId.value}/prs`,
      { page: page.value, limit: limit.value }
    )
    if (response.code === 0) {
      items.value = response.data.items
      total.value = response.data.total
    }
  } finally {
    loading.value = false
  }
}

watch([galgameId, page, limit], load, { immediate: true })

const STATUS: Record<
  number,
  { label: string; color: 'warning' | 'success' | 'danger' }
> = {
  0: { label: 'Pending', color: 'warning' },
  1: { label: 'Merged', color: 'success' },
  2: { label: 'Declined', color: 'danger' }
}

const merge = async (pr: GalgamePR) => {
  if (
    !(await useKunConfirm({
      title: '合并 PR',
      content: `合并 PR #${pr.id}：${pr.title}？`,
      confirmText: '合并'
    }))
  )
    return
  const response = await api.put(
    `/galgame/${galgameId.value}/prs/${pr.id}/merge`
  )
  if (response.code === 0) {
    useKunMessage('合并成功', 'success')
    await load()
  } else {
    useKunMessage(response.message || '合并失败', 'error')
  }
}

const decline = async (pr: GalgamePR) => {
  if (
    !(await useKunConfirm({
      title: '拒绝 PR',
      content: `拒绝 PR #${pr.id}：${pr.title}？`,
      confirmText: '拒绝',
      danger: true
    }))
  )
    return
  const response = await api.put(
    `/galgame/${galgameId.value}/prs/${pr.id}/decline`
  )
  if (response.code === 0) {
    useKunMessage('已拒绝', 'success')
    await load()
  } else {
    useKunMessage(response.message || '操作失败', 'error')
  }
}

const viewDiff = (pr: GalgamePR) => {
  diffPR.value = pr
  diffOpen.value = true
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center gap-3">
      <KunButton
        variant="light"
        size="sm"
        is-icon-only
        aria-label="返回 galgame 详情页"
        @click="router.push(`/galgame/${galgameId}`)"
      >
        <Icon name="lucide:arrow-left" class="size-5" />
      </KunButton>
      <h1 class="text-foreground text-2xl font-bold">
        Pull Requests · galgame #{{ galgameId }}
      </h1>
    </div>

    <KunCard class="overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[56rem] text-sm">
          <thead class="bg-content2 text-default-500 text-left">
            <tr>
              <th class="px-4 py-3 font-medium">#</th>
              <th class="px-4 py-3 font-medium">标题</th>
              <th class="px-4 py-3 font-medium">Base rev</th>
              <th class="px-4 py-3 font-medium">状态</th>
              <th class="px-4 py-3 font-medium">作者</th>
              <th class="px-4 py-3 font-medium">更新</th>
              <th class="px-4 py-3 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="loading && items.length === 0">
              <td colspan="7" class="text-default-400 px-4 py-10 text-center">
                <Icon name="lucide:loader-2" class="inline size-5 animate-spin" />
              </td>
            </tr>
            <tr v-else-if="items.length === 0">
              <td colspan="7" class="text-default-400 px-4 py-10 text-center">
                暂无 PR
              </td>
            </tr>
            <tr
              v-for="pr in items"
              v-else
              :key="pr.id"
              class="border-default-200 hover:bg-default-50 border-t transition-colors"
            >
              <td class="text-foreground px-4 py-2 font-mono">#{{ pr.id }}</td>
              <td class="text-foreground max-w-md px-4 py-2 font-medium">
                {{ pr.title || '(无标题)' }}
                <p
                  v-if="pr.message"
                  class="text-default-400 mt-0.5 truncate text-xs"
                >
                  {{ pr.message }}
                </p>
              </td>
              <td class="text-default-500 px-4 py-2 font-mono text-xs">
                rev {{ pr.base_revision }}
              </td>
              <td class="px-4 py-2">
                <span
                  class="rounded-full px-2 py-0.5 text-xs"
                  :class="{
                    'bg-warning-50 text-warning': STATUS[pr.status]?.color === 'warning',
                    'bg-success-50 text-success': STATUS[pr.status]?.color === 'success',
                    'bg-danger-50 text-danger': STATUS[pr.status]?.color === 'danger'
                  }"
                >
                  {{ STATUS[pr.status]?.label ?? pr.status }}
                </span>
              </td>
              <td class="text-default-500 px-4 py-2 text-xs">id={{ pr.user_id }}</td>
              <td class="text-default-400 px-4 py-2 text-xs">
                {{ pr.updated?.replace('T', ' ').slice(0, 19) }}
              </td>
              <td class="px-4 py-2 text-right">
                <div class="flex justify-end gap-1">
                  <KunButton
                    variant="light"
                    color="primary"
                    size="xs"
                    @click="viewDiff(pr)"
                  >
                    diff
                  </KunButton>
                  <template v-if="pr.status === 0">
                    <KunButton
                      variant="light"
                      color="success"
                      size="xs"
                      @click="merge(pr)"
                    >
                      合并
                    </KunButton>
                    <KunButton
                      variant="light"
                      color="danger"
                      size="xs"
                      @click="decline(pr)"
                    >
                      拒绝
                    </KunButton>
                  </template>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </KunCard>

    <CommonPagination
      :page="page"
      :total="total"
      :limit="limit"
      @update:page="page = $event"
    />

    <GalgamePRDiffModal
      v-if="diffPR"
      :open="diffOpen"
      :pr="diffPR"
      @close="diffOpen = false"
    />
  </div>
</template>
