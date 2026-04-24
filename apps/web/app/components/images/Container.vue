<script setup lang="ts">
import { IMAGE_STATUS_TABS } from '~/constants/admin'

const api = useApi()

const items = ref<ImageAdminRow[]>([])
const total = ref(0)
const isLoading = ref(true)
const site = ref('')
const reviewStatus = ref<string>('')
const currentPage = ref(1)
const limit = 50

const stats = ref<ImageAdminStats | null>(null)

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / limit)))

const fetchList = async () => {
  isLoading.value = true
  try {
    const params = new URLSearchParams({
      page: String(currentPage.value),
      limit: String(limit)
    })
    if (reviewStatus.value) params.set('review_status', reviewStatus.value)
    if (site.value) params.set('site', site.value)

    const res = await api.get<ImageAdminListResponse>(
      `/admin/image/list?${params}`
    )
    if (res.code === 0) {
      items.value = res.data.items || []
      total.value = res.data.total
    }
  } finally {
    isLoading.value = false
  }
}

const fetchStats = async () => {
  const res = await api.get<ImageAdminStats>('/admin/image-stats')
  if (res.code === 0) stats.value = res.data
}

const onReview = async (hash: string, status: string, reason?: string) => {
  const res = await api.patch(`/admin/image/${hash}/review`, { status, reason })
  if (res.code === 0) {
    await fetchList()
    await fetchStats()
  }
}

const onDelete = async (hash: string, force = false) => {
  if (!confirm(force ? '硬删除：物理删除 S3 对象与数据库行，不可恢复。继续？' : '软删除：30 天后 GC 会物理删除。继续？')) {
    return
  }
  const res = await api.delete(
    `/admin/image/${hash}${force ? '?force=true' : ''}`
  )
  if (res.code === 0) {
    await fetchList()
    await fetchStats()
  }
}

const bytesHuman = (n: number) => {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v > 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

watch([currentPage, reviewStatus, site], () => fetchList())

onMounted(() => {
  fetchList()
  fetchStats()
})
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-foreground">图片管理</h1>
        <p class="mt-1 text-default-500">
          共 {{ total }} 张图片
        </p>
      </div>
    </div>

    <!-- Stats cards -->
    <div v-if="stats" class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <div class="rounded-xl bg-content1 p-4 shadow-sm">
        <div class="text-sm text-default-500">总上传次数</div>
        <div class="mt-1 text-2xl font-bold text-foreground">
          {{ stats.upload_count }}
        </div>
      </div>
      <div class="rounded-xl bg-content1 p-4 shadow-sm">
        <div class="text-sm text-default-500">唯一图片</div>
        <div class="mt-1 text-2xl font-bold text-foreground">
          {{ stats.unique_images }}
        </div>
        <div class="text-xs text-default-400">
          去重 {{ stats.deduplicated_count }}
        </div>
      </div>
      <div class="rounded-xl bg-content1 p-4 shadow-sm">
        <div class="text-sm text-default-500">存储用量</div>
        <div class="mt-1 text-2xl font-bold text-foreground">
          {{ bytesHuman(stats.total_bytes) }}
        </div>
      </div>
      <div class="rounded-xl bg-content1 p-4 shadow-sm">
        <div class="text-sm text-default-500">待审 / 已拒</div>
        <div class="mt-1 text-2xl font-bold text-foreground">
          {{ stats.review_pending }} / {{ stats.review_rejected }}
        </div>
      </div>
    </div>

    <!-- Filter bar -->
    <div class="rounded-xl bg-content1 p-4 shadow-sm space-y-3">
      <div class="flex flex-wrap gap-2">
        <KunButton
          v-for="tab in IMAGE_STATUS_TABS"
          :key="tab.id"
          :color="reviewStatus === tab.id ? 'primary' : 'default'"
          :variant="reviewStatus === tab.id ? 'solid' : 'flat'"
          size="sm"
          @click="reviewStatus = tab.id; currentPage = 1"
        >
          <Icon :name="tab.icon" class="mr-1 size-4" />
          {{ tab.label }}
        </KunButton>
      </div>
      <div class="flex gap-3">
        <KunInput
          v-model="site"
          type="text"
          placeholder="按站点过滤 (kungal / moyu / galgame_wiki)"
          class="flex-1"
        />
        <KunButton :disabled="isLoading" @click="currentPage = 1; fetchList()">
          刷新
        </KunButton>
      </div>
    </div>

    <div v-if="isLoading" class="flex items-center justify-center py-12">
      <Icon name="lucide:loader-2" class="size-8 animate-spin text-primary" />
    </div>

    <template v-else>
      <ImagesTable
        :items="items"
        :bytes-human="bytesHuman"
        @review="onReview"
        @delete="onDelete"
      />

      <div v-if="totalPages > 1" class="flex justify-center">
        <KunPagination
          v-model:current-page="currentPage"
          :total-page="totalPages"
          :is-loading="isLoading"
        />
      </div>
    </template>
  </div>
</template>
