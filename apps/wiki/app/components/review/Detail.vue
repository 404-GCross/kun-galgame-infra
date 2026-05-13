<script setup lang="ts">
// Review detail: full galgame info + submitter metadata + 3 admin actions
// (approve / decline / ban). Reads from /admin/galgame/:gid which returns
// any status. After a successful action, navigates back to /review.
import {
  GALGAME_STATUS_MAP,
  CONTENT_LIMIT_MAP,
  TAG_CATEGORY_MAP,
  OFFICIAL_CATEGORY_MAP,
  REVIEW_ACTIONS
} from '~/constants/admin'
import type { Galgame } from '~/shared/types/galgame'
import type { ReviewMessage, ReviewQueueResponse } from '~/shared/types/review'
import { resolveBannerUrl } from '~/shared/utils/resolveImage'

const api = useApi()
const route = useRoute()
const router = useRouter()
const cdnBase = useRuntimeConfig().public.imageCdnBase as string

const gid = computed(() => Number(route.params.gid))
const galgame = ref<Galgame | null>(null)
const submissionMsg = ref<ReviewMessage | null>(null)
const loading = ref(false)
const actionOpen = ref<'approve' | 'decline' | 'ban' | null>(null)

const bannerSrc = computed(() => resolveBannerUrl(galgame.value, { cdnBase }))

const displayName = computed(() => {
  const g = galgame.value
  if (!g) return ''
  return g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw
})

const tagsByCategory = computed(() => {
  const groups: Record<string, { id: number; name: string; spoiler: number }[]> =
    {}
  for (const rel of galgame.value?.tag ?? []) {
    if (!rel.tag) continue
    const cat = rel.tag.category
    if (!groups[cat]) groups[cat] = []
    groups[cat].push({
      id: rel.tag.id,
      name: rel.tag.name,
      spoiler: rel.spoiler_level
    })
  }
  return groups
})

const officials = computed(() =>
  (galgame.value?.official ?? [])
    .map((r) => r.official)
    .filter((o): o is NonNullable<typeof o> => !!o)
)

// Pull the most recent submission/edit message for this gid so we can
// render "提交者 / 时间" header. Admin queue endpoint is filtered by
// status=3, so we only need to look in that range.
const loadSubmission = async () => {
  // Server-side filter is by type only; we still need to find the row
  // matching our galgame. Page through up to 100 (admin queue is small).
  const resp = await api.get<ReviewQueueResponse>('/admin/galgame/messages', {
    type: 'submitted,edited_pending',
    page: 1,
    limit: 100
  })
  if (resp.code === 0) {
    submissionMsg.value =
      resp.data.items.find((m) => m.galgame_id === gid.value) ?? null
  }
}

const loadGalgame = async () => {
  loading.value = true
  try {
    const response = await api.get<Galgame>(`/admin/galgame/${gid.value}`)
    if (response.code === 0) {
      galgame.value = response.data
    }
  } finally {
    loading.value = false
  }
}

watch(gid, async () => {
  await Promise.all([loadGalgame(), loadSubmission()])
}, { immediate: true })

const onActionConfirmed = async () => {
  actionOpen.value = null
  // Action modal posts directly; here we just leave the detail page.
  await router.push('/review')
}

const currentAction = computed(() =>
  REVIEW_ACTIONS.find((a) => a.id === actionOpen.value) ?? null
)

const relative = (iso?: string) => {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  const diff = Math.max(0, Date.now() - t) / 1000
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  return `${Math.floor(diff / 86400)} 天前`
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center gap-3">
      <NuxtLink to="/review">
        <KunButton variant="light">
          <Icon name="lucide:arrow-left" class="mr-1 size-4" />
          返回队列
        </KunButton>
      </NuxtLink>
      <h1 class="text-foreground text-2xl font-bold">审核详情</h1>
    </div>

    <div
      v-if="loading"
      class="text-default-400 flex items-center justify-center py-16"
    >
      <Icon name="lucide:loader-2" class="size-6 animate-spin" />
    </div>

    <div v-else-if="!galgame" class="text-default-500">galgame 不存在</div>

    <div v-else class="space-y-6">
      <!-- Submission metadata -->
      <div
        v-if="submissionMsg"
        class="bg-content2 border-default-200 flex items-center gap-4 rounded-lg border p-4"
      >
        <KunAvatar
          v-if="submissionMsg.actor"
          :user="{
            id: submissionMsg.actor.id,
            name: submissionMsg.actor.name,
            avatar: submissionMsg.actor.avatar
          }"
          size="sm"
          :is-navigation="false"
        />
        <div class="flex flex-col">
          <span class="text-foreground font-medium">
            {{ submissionMsg.actor?.name ?? '(未知)' }}
            <span class="text-default-500 ml-2 text-sm">
              {{
                submissionMsg.type === 'submitted'
                  ? '提交了此条目'
                  : '修订了此条目'
              }}
            </span>
          </span>
          <span class="text-default-400 text-xs">
            {{ relative(submissionMsg.created_at) }} ·
            {{ new Date(submissionMsg.created_at).toLocaleString() }}
          </span>
        </div>
      </div>

      <!-- Header card: banner + title + status + actions -->
      <div class="bg-content1 rounded-lg p-6 shadow-sm">
        <div class="flex gap-6">
          <div class="shrink-0">
            <img
              v-if="bannerSrc"
              :src="bannerSrc"
              :alt="displayName"
              class="border-default-200 h-48 w-32 rounded border object-cover"
            >
            <div
              v-else
              class="bg-default-100 text-default-400 flex h-48 w-32 items-center justify-center rounded"
            >
              <Icon name="lucide:image-off" class="size-8" />
            </div>
          </div>
          <div class="flex flex-1 flex-col gap-3">
            <div class="flex items-start justify-between">
              <div>
                <h2 class="text-foreground text-2xl font-bold">{{ displayName }}</h2>
                <div class="text-default-500 mt-1 space-y-0.5 text-sm">
                  <div v-if="galgame.name_ja_jp">{{ galgame.name_ja_jp }}</div>
                  <div v-if="galgame.name_en_us">{{ galgame.name_en_us }}</div>
                  <div v-if="galgame.name_zh_tw">{{ galgame.name_zh_tw }}</div>
                </div>
              </div>
              <div class="flex flex-col items-end gap-2">
                <KunBadge
                  :color="(GALGAME_STATUS_MAP[galgame.status]?.color as any) ?? 'default'"
                >
                  {{ GALGAME_STATUS_MAP[galgame.status]?.label ?? galgame.status }}
                </KunBadge>
                <KunBadge
                  v-if="CONTENT_LIMIT_MAP[galgame.content_limit]"
                  :color="(CONTENT_LIMIT_MAP[galgame.content_limit]!.color as any)"
                >
                  {{ CONTENT_LIMIT_MAP[galgame.content_limit]!.label }}
                </KunBadge>
              </div>
            </div>

            <dl class="text-default-500 grid grid-cols-2 gap-2 text-sm">
              <div>
                <dt class="text-default-400">VNDB ID</dt>
                <dd>{{ galgame.vndb_id || '(无)' }}</dd>
              </div>
              <div>
                <dt class="text-default-400">原始语言</dt>
                <dd>{{ galgame.original_language }}</dd>
              </div>
              <div>
                <dt class="text-default-400">分级</dt>
                <dd>{{ galgame.age_limit }}</dd>
              </div>
              <div>
                <dt class="text-default-400">galgame id</dt>
                <dd>{{ galgame.id }}</dd>
              </div>
            </dl>

            <!-- Action buttons -->
            <div class="border-default-200 mt-2 flex flex-wrap gap-2 border-t pt-3">
              <KunButton
                v-for="action in REVIEW_ACTIONS"
                :key="action.id"
                :color="(action.color as any)"
                @click="actionOpen = action.id"
              >
                <Icon :name="action.icon" class="mr-1 size-4" />
                {{ action.label }}
              </KunButton>
            </div>
          </div>
        </div>
      </div>

      <!-- Aliases -->
      <div
        v-if="galgame.alias?.length"
        class="bg-content1 rounded-lg p-6 shadow-sm"
      >
        <h3 class="text-foreground mb-3 font-semibold">别名</h3>
        <div class="flex flex-wrap gap-2">
          <KunBadge
            v-for="a in galgame.alias"
            :key="a.id"
            color="default"
            variant="flat"
          >
            {{ a.name }}
          </KunBadge>
        </div>
      </div>

      <!-- Tags by category -->
      <div
        v-if="Object.keys(tagsByCategory).length"
        class="bg-content1 rounded-lg p-6 shadow-sm"
      >
        <h3 class="text-foreground mb-3 font-semibold">标签</h3>
        <div class="space-y-3">
          <div v-for="(tags, cat) in tagsByCategory" :key="cat">
            <span class="text-default-400 mr-2 text-sm">
              {{ TAG_CATEGORY_MAP[cat]?.label ?? cat }}:
            </span>
            <span class="inline-flex flex-wrap gap-1">
              <KunBadge
                v-for="t in tags"
                :key="t.id"
                :color="(TAG_CATEGORY_MAP[cat]?.color as any) ?? 'default'"
                variant="flat"
              >
                {{ t.name }}
              </KunBadge>
            </span>
          </div>
        </div>
      </div>

      <!-- Officials -->
      <div v-if="officials.length" class="bg-content1 rounded-lg p-6 shadow-sm">
        <h3 class="text-foreground mb-3 font-semibold">开发商 / 发行商</h3>
        <div class="flex flex-wrap gap-2">
          <KunBadge
            v-for="o in officials"
            :key="o.id"
            :color="(OFFICIAL_CATEGORY_MAP[o.category]?.color as any) ?? 'default'"
            variant="flat"
          >
            {{ o.name }}
          </KunBadge>
        </div>
      </div>

      <!-- Intros -->
      <div
        v-for="lang in ['zh_cn', 'ja_jp', 'en_us', 'zh_tw']"
        :key="lang"
        class="bg-content1 rounded-lg p-6 shadow-sm"
      >
        <h3 class="text-foreground mb-3 font-semibold">简介（{{ lang }}）</h3>
        <pre
          class="text-default-600 whitespace-pre-wrap text-sm"
        >{{ (galgame as any)[`intro_${lang}`] || '(无)' }}</pre>
      </div>
    </div>

    <ReviewActionModal
      v-if="currentAction && galgame"
      :open="!!currentAction"
      :galgame-id="galgame.id"
      :action="currentAction"
      @close="actionOpen = null"
      @done="onActionConfirmed"
    />
  </div>
</template>
