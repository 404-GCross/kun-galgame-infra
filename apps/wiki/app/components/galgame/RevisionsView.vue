<script setup lang="ts">
import type { GalgameRevision } from '~/shared/types/galgame'

const api = useApi()
const route = useRoute()
const router = useRouter()

const galgameId = computed(() => Number(route.params.id))

const page = useQueryState('page', 1)
const limit = ref(20)
const includeMinor = useQueryState('include_minor', '')

const items = ref<GalgameRevision[]>([])
const total = ref(0)
const loading = ref(false)

const diffOpen = ref(false)
const diffRev = ref<number | null>(null)

const load = async () => {
  loading.value = true
  try {
    const response = await api.get<{
      items: GalgameRevision[]
      total: number
    }>(`/galgame/${galgameId.value}/revisions`, {
      page: page.value,
      limit: limit.value,
      include_minor: includeMinor.value === '1' ? true : undefined
    })
    if (response.code === 0) {
      items.value = response.data.items
      total.value = response.data.total
    }
  } finally {
    loading.value = false
  }
}

watch([galgameId, page, limit, includeMinor], load, { immediate: true })

const revert = async (revision: number) => {
  if (
    !(await useKunConfirm({
      title: '回滚版本',
      content: `确认把 galgame 回滚到 rev ${revision}？将创建新的 revision。`,
      confirmText: '回滚'
    }))
  )
    return
  const response = await api.post(`/galgame/${galgameId.value}/revert`, {
    revision
  })
  if (response.code === 0) {
    useKunMessage('回滚成功', 'success')
    await load()
  } else {
    useKunMessage(response.message || '回滚失败', 'error')
  }
}

const openDiff = (revision: number) => {
  diffRev.value = revision
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
        <KunIcon name="lucide:arrow-left" class="size-5" />
      </KunButton>
      <h1 class="text-foreground text-2xl font-bold">
        修订历史 · galgame #{{ galgameId }}
      </h1>
    </div>

    <KunCheckBox
      :model-value="includeMinor === '1'"
      label="包含小改动 (minor revisions)"
      @update:model-value="(v: boolean) => (includeMinor = v ? '1' : '')"
    />

    <KunCard class="overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[52rem] text-sm">
          <thead class="bg-content2 text-default-500 text-left">
            <tr>
              <th class="px-4 py-3 font-medium">Rev</th>
              <th class="px-4 py-3 font-medium">Message</th>
              <th class="px-4 py-3 font-medium">类型</th>
              <th class="px-4 py-3 font-medium">用户</th>
              <th class="px-4 py-3 font-medium">时间</th>
              <th class="px-4 py-3 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="loading && items.length === 0">
              <td colspan="6" class="text-default-400 px-4 py-10 text-center">
                <KunIcon name="lucide:loader-circle" class="inline size-5 animate-spin" />
              </td>
            </tr>
            <tr v-else-if="items.length === 0">
              <td colspan="6" class="text-default-400 px-4 py-10 text-center">
                暂无 revision
              </td>
            </tr>
            <tr
              v-for="r in items"
              v-else
              :key="r.id"
              class="border-default-200 hover:bg-default-50 border-t transition-colors"
            >
              <td class="text-foreground px-4 py-2 font-mono font-medium">
                #{{ r.revision }}
              </td>
              <td class="text-foreground max-w-md px-4 py-2">
                {{ r.note || '—' }}
              </td>
              <td class="px-4 py-2">
                <span
                  v-if="r.is_minor"
                  class="bg-default-100 text-default-500 rounded px-2 py-0.5 text-xs"
                >
                  minor
                </span>
                <span
                  v-else
                  class="bg-primary-50 text-primary rounded px-2 py-0.5 text-xs"
                >
                  normal
                </span>
              </td>
              <td class="text-default-500 px-4 py-2 text-xs">
                id={{ r.user_id }}
              </td>
              <td class="text-default-400 px-4 py-2 text-xs">
                {{ r.created?.replace('T', ' ').slice(0, 19) }}
              </td>
              <td class="px-4 py-2 text-right">
                <div class="flex justify-end gap-1">
                  <KunButton
                    variant="light"
                    color="primary"
                    size="xs"
                    @click="openDiff(r.revision)"
                  >
                    diff
                  </KunButton>
                  <KunButton
                    variant="light"
                    color="warning"
                    size="xs"
                    @click="revert(r.revision)"
                  >
                    回滚到此
                  </KunButton>
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

    <GalgameRevisionDiffModal
      v-if="diffRev !== null"
      :open="diffOpen"
      :galgame-id="galgameId"
      :revision="diffRev"
      @close="diffOpen = false"
    />
  </div>
</template>
