<script setup lang="ts">
import {
  GALGAME_STATUS_MAP,
  CONTENT_LIMIT_MAP,
  TAG_CATEGORY_MAP,
  OFFICIAL_CATEGORY_MAP
} from '~/constants/admin'
import type { Galgame } from '~/shared/types/galgame'

const api = useApi()
const route = useRoute()
const router = useRouter()

const id = computed(() => Number(route.params.id))
const galgame = ref<Galgame | null>(null)
const loading = ref(false)
const updating = ref(false)
const editOpen = ref(false)

const load = async () => {
  loading.value = true
  try {
    const response = await api.get<Galgame>(`/admin/galgame/${id.value}`)
    if (response.code === 0) {
      galgame.value = response.data
    }
  } finally {
    loading.value = false
  }
}

watch(id, load, { immediate: true })

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

const changeStatus = async (newStatus: number) => {
  if (!galgame.value) return
  if (newStatus === galgame.value.status) return

  const actionMap: Record<number, string> = {
    0: '发布',
    1: '封禁',
    2: '撤回为草稿'
  }
  const action = actionMap[newStatus]
  if (!confirm(`确认将该 galgame ${action} 吗？`)) return

  updating.value = true
  try {
    const response = await api.put(`/admin/galgame/${id.value}/status`, {
      status: newStatus
    })
    if (response.code === 0) {
      useKunMessage(`${action}成功`, 'success')
      await load()
    } else {
      useKunMessage(response.message || `${action}失败`, 'error')
    }
  } finally {
    updating.value = false
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center gap-3">
      <button
        class="text-default-500 hover:bg-default-100 rounded-lg p-2 transition-colors"
        @click="router.back()"
      >
        <Icon name="lucide:arrow-left" class="size-5" />
      </button>
      <h1 class="text-foreground text-2xl font-bold">Galgame 详情</h1>
    </div>

    <div
      v-if="loading && !galgame"
      class="text-default-400 flex items-center justify-center py-20"
    >
      <Icon name="lucide:loader-2" class="size-6 animate-spin" />
    </div>

    <div v-else-if="!galgame" class="text-default-400 py-20 text-center">
      未找到该 galgame
    </div>

    <template v-else>
      <KunCard class="p-6">
        <div class="flex gap-6">
          <img
            v-if="galgame.banner"
            :src="galgame.banner"
            class="bg-default-100 h-48 w-36 flex-shrink-0 rounded object-cover"
            alt=""
          />
          <div
            v-else
            class="bg-default-100 flex h-48 w-36 flex-shrink-0 items-center justify-center rounded"
          >
            <Icon name="lucide:image" class="text-default-300 size-10" />
          </div>

          <div class="flex-1 space-y-3">
            <div>
              <h2 class="text-foreground text-xl font-bold">{{ displayName }}</h2>
              <p
                v-if="galgame.name_ja_jp && galgame.name_ja_jp !== displayName"
                class="text-default-500"
              >
                {{ galgame.name_ja_jp }}
              </p>
              <p
                v-if="galgame.name_en_us && galgame.name_en_us !== displayName"
                class="text-default-400 text-sm"
              >
                {{ galgame.name_en_us }}
              </p>
            </div>

            <div class="flex flex-wrap items-center gap-2 text-sm">
              <span
                class="rounded-full px-2 py-0.5 text-xs"
                :class="{
                  'bg-success-50 text-success':
                    GALGAME_STATUS_MAP[galgame.status]?.color === 'success',
                  'bg-warning-50 text-warning':
                    GALGAME_STATUS_MAP[galgame.status]?.color === 'warning',
                  'bg-danger-50 text-danger':
                    GALGAME_STATUS_MAP[galgame.status]?.color === 'danger'
                }"
              >
                {{ GALGAME_STATUS_MAP[galgame.status]?.label }}
              </span>
              <span
                class="rounded px-2 py-0.5 text-xs"
                :class="
                  CONTENT_LIMIT_MAP[galgame.content_limit]?.color === 'danger'
                    ? 'bg-danger-50 text-danger'
                    : 'bg-default-100 text-default-500'
                "
              >
                {{ CONTENT_LIMIT_MAP[galgame.content_limit]?.label }}
              </span>
              <span class="text-default-500">
                发售：{{ galgame.released }}
              </span>
              <span class="text-default-500">
                语言：{{ galgame.original_language }}
              </span>
              <a
                :href="`https://vndb.org/${galgame.vndb_id}`"
                target="_blank"
                class="text-primary font-mono text-xs hover:underline"
              >
                {{ galgame.vndb_id }}
              </a>
            </div>

            <div class="flex flex-wrap gap-2 pt-2">
              <KunButton variant="light" @click="editOpen = true">
                <Icon name="lucide:pencil" class="mr-1 size-4" />
                编辑
              </KunButton>
              <NuxtLink :to="`/galgame/${galgame.id}/revisions`">
                <KunButton variant="light">
                  <Icon name="lucide:history" class="mr-1 size-4" />
                  修订历史
                </KunButton>
              </NuxtLink>
              <NuxtLink :to="`/galgame/${galgame.id}/prs`">
                <KunButton variant="light">
                  <Icon name="lucide:git-pull-request" class="mr-1 size-4" />
                  PR
                </KunButton>
              </NuxtLink>
              <KunButton
                v-if="galgame.status !== 0"
                color="success"
                :disabled="updating"
                @click="changeStatus(0)"
              >
                <Icon name="lucide:check" class="mr-1 size-4" />
                发布
              </KunButton>
              <KunButton
                v-if="galgame.status !== 2"
                variant="light"
                :disabled="updating"
                @click="changeStatus(2)"
              >
                <Icon name="lucide:file-edit" class="mr-1 size-4" />
                撤回草稿
              </KunButton>
              <KunButton
                v-if="galgame.status !== 1"
                color="danger"
                variant="light"
                :disabled="updating"
                @click="changeStatus(1)"
              >
                <Icon name="lucide:ban" class="mr-1 size-4" />
                封禁
              </KunButton>
            </div>
          </div>
        </div>
      </KunCard>

      <KunCard v-if="galgame.intro_zh_cn || galgame.intro_ja_jp || galgame.intro_en_us" class="p-6">
        <h3 class="text-foreground mb-3 text-base font-semibold">简介</h3>
        <div class="text-default-600 space-y-3 text-sm whitespace-pre-wrap">
          <div v-if="galgame.intro_zh_cn">
            <p class="text-default-400 mb-1 text-xs">简体中文</p>
            <p>{{ galgame.intro_zh_cn }}</p>
          </div>
          <div v-if="galgame.intro_ja_jp">
            <p class="text-default-400 mb-1 text-xs">日本語</p>
            <p>{{ galgame.intro_ja_jp }}</p>
          </div>
          <div v-if="galgame.intro_en_us">
            <p class="text-default-400 mb-1 text-xs">English</p>
            <p>{{ galgame.intro_en_us }}</p>
          </div>
        </div>
      </KunCard>

      <KunCard v-if="galgame.alias && galgame.alias.length > 0" class="p-6">
        <h3 class="text-foreground mb-3 text-base font-semibold">别名</h3>
        <div class="flex flex-wrap gap-2">
          <span
            v-for="a in galgame.alias"
            :key="a.id"
            class="bg-default-100 text-default-600 rounded px-2 py-1 text-xs"
          >
            {{ a.name }}
          </span>
        </div>
      </KunCard>

      <KunCard class="p-6">
        <h3 class="text-foreground mb-3 text-base font-semibold">
          标签 ({{ galgame.tag?.length ?? 0 }})
        </h3>
        <div v-if="!galgame.tag || galgame.tag.length === 0" class="text-default-400 text-sm">
          暂无标签
        </div>
        <div v-else class="space-y-3">
          <div v-for="(tags, cat) in tagsByCategory" :key="cat">
            <p class="text-default-400 mb-2 text-xs">
              {{ TAG_CATEGORY_MAP[cat]?.label ?? cat }} ({{ tags.length }})
            </p>
            <div class="flex flex-wrap gap-2">
              <span
                v-for="t in tags"
                :key="t.id"
                class="rounded-full px-2 py-1 text-xs"
                :class="{
                  'bg-primary-50 text-primary':
                    TAG_CATEGORY_MAP[cat]?.color === 'primary',
                  'bg-secondary-50 text-secondary':
                    TAG_CATEGORY_MAP[cat]?.color === 'secondary',
                  'bg-info-50 text-info':
                    TAG_CATEGORY_MAP[cat]?.color === 'info'
                }"
              >
                {{ t.name }}
                <span v-if="t.spoiler > 0" class="ml-1 opacity-60">
                  (spoil {{ t.spoiler }})
                </span>
              </span>
            </div>
          </div>
        </div>
      </KunCard>

      <KunCard class="p-6">
        <h3 class="text-foreground mb-3 text-base font-semibold">
          会社 ({{ officials.length }})
        </h3>
        <div v-if="officials.length === 0" class="text-default-400 text-sm">
          暂无
        </div>
        <div v-else class="space-y-2">
          <div
            v-for="o in officials"
            :key="o.id"
            class="border-default-200 flex items-center justify-between rounded-lg border p-3"
          >
            <div>
              <p class="text-foreground font-medium">{{ o.name }}</p>
              <p
                v-if="o.original && o.original !== o.name"
                class="text-default-500 text-sm"
              >
                {{ o.original }}
              </p>
            </div>
            <div class="flex items-center gap-2 text-xs">
              <span
                class="rounded px-2 py-0.5"
                :class="{
                  'bg-primary-50 text-primary':
                    OFFICIAL_CATEGORY_MAP[o.category]?.color === 'primary',
                  'bg-info-50 text-info':
                    OFFICIAL_CATEGORY_MAP[o.category]?.color === 'info',
                  'bg-default-100 text-default-500':
                    OFFICIAL_CATEGORY_MAP[o.category]?.color === 'default'
                }"
              >
                {{ OFFICIAL_CATEGORY_MAP[o.category]?.label ?? o.category }}
              </span>
              <span class="text-default-400">{{ o.lang }}</span>
            </div>
          </div>
        </div>
      </KunCard>

      <KunCard v-if="galgame.series" class="p-6">
        <h3 class="text-foreground mb-3 text-base font-semibold">所属系列</h3>
        <NuxtLink
          :to="`/series/${galgame.series.id}`"
          class="text-primary hover:underline"
        >
          {{ galgame.series.name }}
        </NuxtLink>
        <p v-if="galgame.series.description" class="text-default-500 mt-1 text-sm">
          {{ galgame.series.description }}
        </p>
      </KunCard>

      <GalgameAliasesSection :galgame-id="galgame.id" />
      <GalgameLinksSection :galgame-id="galgame.id" />
      <GalgameContributorsSection :galgame-id="galgame.id" />

      <GalgameEditModal
        :open="editOpen"
        :galgame="galgame"
        @close="editOpen = false"
        @saved="
          () => {
            editOpen = false
            load()
          }
        "
      />
    </template>
  </div>
</template>
