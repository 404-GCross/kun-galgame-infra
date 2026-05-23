<script setup lang="ts">
import {
  GALGAME_STATUS_MAP,
  CONTENT_LIMIT_MAP,
  TAG_CATEGORY_MAP,
  OFFICIAL_CATEGORY_MAP
} from '~/constants/admin'
import type { Galgame } from '~/shared/types/galgame'
import { resolveBannerUrl } from '~/shared/utils/resolveImage'
import { formatReleaseDate } from '~/shared/utils/format'

const api = useApi()
const route = useRoute()
const router = useRouter()
const cdnBase = useRuntimeConfig().public.imageCdnBase as string

const id = computed(() => Number(route.params.id))
const updating = ref(false)
const editOpen = ref(false)

// SSR: server-fetched (token cookie is non-httpOnly so useApi can attach
// it server-side), payload-hydrated → no post-hydration flash for the
// common authed case. Admin-only endpoint; soft-fails server-side if
// unauthorized, the onMounted guard re-fetches client-side where token
// refresh works.
const { data: galgame, status, refresh } = await useAsyncData(
  'galgame-detail',
  async () => {
    const r = await api.get<Galgame>(`/admin/galgame/${id.value}`)
    return r.code === 0 ? r.data : null
  },
  { watch: [id] }
)
const loading = computed(() => status.value === 'pending')

onMounted(() => {
  if (!galgame.value) refresh()
})

// ────────────────────────────────────────────────
// Derived display data
// ────────────────────────────────────────────────
const bannerSrc = computed(() =>
  resolveBannerUrl(galgame.value, { cdnBase })
)

const displayName = computed(() => {
  const g = galgame.value
  if (!g) return ''
  return g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw
})

const tagsByCategory = computed(() => {
  const groups: Record<
    string,
    { id: number; name: string; spoiler: number }[]
  > = {}
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

const intros = computed(() => {
  const g = galgame.value
  if (!g) return [] as { lang: string; label: string; text: string }[]
  return (
    [
      { lang: 'zh_cn', label: '简体中文', text: g.intro_zh_cn },
      { lang: 'ja_jp', label: '日本語', text: g.intro_ja_jp },
      { lang: 'en_us', label: 'English', text: g.intro_en_us },
      { lang: 'zh_tw', label: '繁體中文', text: g.intro_zh_tw }
    ] as const
  ).filter((i): i is { lang: string; label: string; text: string } =>
    Boolean(i.text)
  )
})

// Default intro language → first non-empty one. State persists across
// the page lifetime; switching tabs doesn't reset.
const introLang = ref<string>('')
watchEffect(() => {
  if (intros.value.length && !introLang.value) {
    introLang.value = intros.value[0]!.lang
  }
})
const currentIntro = computed(
  () => intros.value.find((i) => i.lang === introLang.value)?.text ?? ''
)

// ────────────────────────────────────────────────
// Actions
// ────────────────────────────────────────────────
const changeStatus = async (newStatus: number) => {
  if (!galgame.value) return
  if (newStatus === galgame.value.status) return

  const actionMap: Record<number, string> = {
    0: '发布',
    1: '封禁',
    2: '撤回为草稿'
  }
  const action = actionMap[newStatus]
  if (
    !(await useKunConfirm({
      title: `${action}确认`,
      content: `确认将该 galgame ${action} 吗？`,
      confirmText: action,
      danger: newStatus === 1
    }))
  )
    return

  updating.value = true
  try {
    const response = await api.put(`/admin/galgame/${id.value}/status`, {
      status: newStatus
    })
    if (response.code === 0) {
      useKunMessage(`${action}成功`, 'success')
      await refresh()
    } else {
      useKunMessage(response.message || `${action}失败`, 'error')
    }
  } finally {
    updating.value = false
  }
}

// ────────────────────────────────────────────────
// Tabs
// ────────────────────────────────────────────────
type TabId =
  | 'overview'
  | 'tags'
  | 'officials'
  | 'aliases'
  | 'links'
  | 'contributors'

const TABS: { id: TabId; label: string; icon: string; count?: () => number }[] =
  [
    { id: 'overview', label: '基本', icon: 'lucide:info' },
    {
      id: 'tags',
      label: '标签',
      icon: 'lucide:tags',
      count: () => galgame.value?.tag?.length ?? 0
    },
    {
      id: 'officials',
      label: '会社',
      icon: 'lucide:building-2',
      count: () => officials.value.length
    },
    {
      id: 'aliases',
      label: '别名',
      icon: 'lucide:type',
      count: () => galgame.value?.alias?.length ?? 0
    },
    { id: 'links', label: '链接', icon: 'lucide:link' },
    { id: 'contributors', label: '贡献者', icon: 'lucide:users' }
  ]

const activeTab = ref<TabId>('overview')

// Status chip styling — declared as a static lookup so Tailwind JIT
// picks every color combo up (don't construct class strings at runtime).
const STATUS_CHIP_BG: Record<string, string> = {
  success: 'bg-success-50 text-success border-success-200',
  warning: 'bg-warning-50 text-warning border-warning-200',
  danger: 'bg-danger-50 text-danger border-danger-200'
}
const statusChipClass = computed(() => {
  const color = GALGAME_STATUS_MAP[galgame.value?.status ?? 0]?.color
  return STATUS_CHIP_BG[color ?? ''] ?? 'bg-default-100 text-default-500'
})
</script>

<template>
  <div class="space-y-4">
    <!-- Top thin bar: back + page title + global status chip -->
    <div class="flex items-center justify-between gap-3">
      <div class="flex items-center gap-3">
        <KunButton
          variant="light"
          size="sm"
          is-icon-only
          aria-label="返回"
          @click="router.back()"
        >
          <Icon name="lucide:arrow-left" class="size-5" />
        </KunButton>
        <h1 class="text-foreground text-2xl font-bold">Galgame 详情</h1>
        <span
          v-if="galgame"
          class="text-default-400 font-mono text-sm"
        >
          #{{ galgame.id }}
        </span>
      </div>
      <span
        v-if="galgame"
        :class="
          cn(
            'inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium',
            statusChipClass
          )
        "
      >
        <Icon name="lucide:circle-dot" class="size-3" />
        {{ GALGAME_STATUS_MAP[galgame.status]?.label }}
      </span>
    </div>

    <div
      v-if="loading && !galgame"
      class="text-default-400 flex items-center justify-center py-20"
    >
      <Icon name="lucide:loader-2" class="size-6 animate-spin" />
    </div>

    <div
      v-else-if="!galgame"
      class="text-default-400 py-20 text-center"
    >
      未找到该 galgame
    </div>

    <!-- ========== Admin Workbench: sidebar + tab content ========== -->
    <div
      v-else
      class="grid grid-cols-1 gap-4 lg:grid-cols-[320px_1fr]"
    >
      <!-- ============ Sticky sidebar ============ -->
      <aside class="space-y-3 lg:sticky lg:top-4 lg:self-start">
        <!-- Banner thumbnail (fixed aspect to keep sidebar geometry
             stable regardless of image size) -->
        <div
          class="bg-default-100 border-default-200 relative overflow-hidden rounded-xl border"
          style="aspect-ratio: 16 / 9"
        >
          <img
            v-if="bannerSrc"
            :src="bannerSrc"
            class="size-full object-cover"
            :alt="displayName"
          />
          <div
            v-else
            class="flex size-full items-center justify-center"
          >
            <Icon name="lucide:image" class="text-default-300 size-10" />
          </div>
          <!-- Content-limit chip overlaid on banner -->
          <span
            class="absolute top-2 left-2 inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium"
            :class="
              CONTENT_LIMIT_MAP[galgame.content_limit]?.color === 'danger'
                ? 'bg-danger text-white'
                : 'bg-success text-white'
            "
          >
            {{ CONTENT_LIMIT_MAP[galgame.content_limit]?.label }}
          </span>
        </div>

        <!-- Title block -->
        <div class="space-y-1">
          <h2 class="text-foreground text-lg font-bold leading-snug">
            {{ displayName }}
          </h2>
          <p
            v-if="
              galgame.name_ja_jp && galgame.name_ja_jp !== displayName
            "
            class="text-default-600 text-sm"
          >
            {{ galgame.name_ja_jp }}
          </p>
          <p
            v-if="
              galgame.name_en_us && galgame.name_en_us !== displayName
            "
            class="text-default-400 text-xs"
          >
            {{ galgame.name_en_us }}
          </p>
        </div>

        <!-- Metadata key/value list -->
        <dl
          class="text-default-500 border-default-200 grid grid-cols-[auto_1fr] gap-x-3 gap-y-2 rounded-xl border p-3 text-xs"
        >
          <dt>VNDB</dt>
          <dd class="text-right">
            <a
              :href="`https://vndb.org/${galgame.vndb_id}`"
              target="_blank"
              class="text-primary font-mono hover:underline"
            >
              {{ galgame.vndb_id }}
              <Icon name="lucide:external-link" class="ml-0.5 inline size-3" />
            </a>
          </dd>
          <dt>语言</dt>
          <dd class="text-foreground text-right">
            {{ galgame.original_language }}
          </dd>
          <dt>发售</dt>
          <dd class="text-foreground text-right">
            {{
              formatReleaseDate(
                galgame.release_date,
                galgame.release_date_tba
              )
            }}
          </dd>
          <dt>年龄</dt>
          <dd class="text-right">
            <span
              :class="
                galgame.age_limit === 'r18'
                  ? 'bg-danger-50 text-danger rounded px-1.5 py-0.5 text-xs font-medium'
                  : 'bg-success-50 text-success rounded px-1.5 py-0.5 text-xs font-medium'
              "
            >
              {{ galgame.age_limit === 'r18' ? 'R18' : '全年龄' }}
            </span>
          </dd>
        </dl>

        <!-- Primary actions: edit / history / pr -->
        <div class="flex flex-col gap-2">
          <KunButton
            variant="solid"
            color="primary"
            full-width
            @click="editOpen = true"
          >
            <Icon name="lucide:pencil" class="mr-1 size-4" />
            编辑
          </KunButton>
          <div class="grid grid-cols-2 gap-2">
            <NuxtLink :to="`/galgame/${galgame.id}/revisions`">
              <KunButton variant="flat" size="sm" full-width>
                <Icon name="lucide:history" class="mr-1 size-4" />
                修订
              </KunButton>
            </NuxtLink>
            <NuxtLink :to="`/galgame/${galgame.id}/prs`">
              <KunButton variant="flat" size="sm" full-width>
                <Icon
                  name="lucide:git-pull-request"
                  class="mr-1 size-4"
                />
                PR
              </KunButton>
            </NuxtLink>
          </div>
        </div>

        <!-- Status change actions — separated by divider to signal they
             affect publication state -->
        <div class="border-default-200 space-y-2 border-t pt-3">
          <p class="text-default-400 text-xs">状态变更</p>
          <div class="flex flex-col gap-2">
            <KunButton
              v-if="galgame.status !== 0"
              color="success"
              variant="flat"
              size="sm"
              full-width
              :disabled="updating"
              @click="changeStatus(0)"
            >
              <Icon name="lucide:check-circle" class="mr-1 size-4" />
              发布
            </KunButton>
            <KunButton
              v-if="galgame.status !== 2"
              variant="flat"
              size="sm"
              full-width
              :disabled="updating"
              @click="changeStatus(2)"
            >
              <Icon name="lucide:file-edit" class="mr-1 size-4" />
              撤回草稿
            </KunButton>
            <KunButton
              v-if="galgame.status !== 1"
              color="danger"
              variant="flat"
              size="sm"
              full-width
              :disabled="updating"
              @click="changeStatus(1)"
            >
              <Icon name="lucide:ban" class="mr-1 size-4" />
              封禁
            </KunButton>
          </div>
        </div>
      </aside>

      <!-- ============ Tab content area ============ -->
      <div class="space-y-4">
        <!-- Tab bar — sticky under the page header so it follows long
             scrolling content. Each tab shows an optional count chip. -->
        <nav
          class="bg-background/85 border-default-200 sticky top-0 z-20 -mx-2 border-b backdrop-blur-md"
          aria-label="详情分区"
        >
          <div class="flex items-center gap-1 overflow-x-auto px-2 py-2">
            <button
              v-for="tab in TABS"
              :key="tab.id"
              type="button"
              :class="
                cn(
                  'group relative flex shrink-0 items-center gap-2 rounded-lg px-3 py-2 text-sm font-medium transition-colors',
                  'focus:outline-none focus:ring-2 focus:ring-primary/40',
                  activeTab === tab.id
                    ? 'text-primary'
                    : 'text-default-500 hover:text-foreground'
                )
              "
              :aria-current="activeTab === tab.id ? 'page' : undefined"
              @click="activeTab = tab.id"
            >
              <Icon :name="tab.icon" class="size-4 shrink-0" />
              <span>{{ tab.label }}</span>
              <span
                v-if="tab.count"
                :class="
                  cn(
                    'rounded-full px-1.5 py-0.5 text-xs',
                    activeTab === tab.id
                      ? 'bg-primary/15 text-primary'
                      : 'bg-default-100 text-default-500'
                  )
                "
              >
                {{ tab.count() }}
              </span>
              <span
                aria-hidden="true"
                :class="
                  cn(
                    'bg-primary absolute right-2 bottom-0 left-2 h-0.5 rounded-full transition-opacity',
                    activeTab === tab.id ? 'opacity-100' : 'opacity-0'
                  )
                "
              />
            </button>
          </div>
        </nav>

        <!-- Tab panel — Transition between content with subtle fade so
             user perceives the tab switch -->
        <Transition
          mode="out-in"
          enter-active-class="transition-opacity duration-150 ease-out"
          enter-from-class="opacity-0"
          leave-active-class="transition-opacity duration-100 ease-in"
          leave-to-class="opacity-0"
        >
          <!-- ============ Tab: 基本 ============ -->
          <div v-if="activeTab === 'overview'" key="overview" class="space-y-4">
            <KunCard
              v-if="intros.length"
              :is-hoverable="false"
              :is-transparent="false"
              content-class="space-y-3"
            >
              <div class="flex items-center justify-between">
                <h3 class="text-foreground text-base font-semibold">简介</h3>
                <!-- Language switcher — small pill row so user can see
                     all 4 languages quickly. Uses the same lookup table
                     as the body content below. -->
                <div class="flex gap-1">
                  <button
                    v-for="lang in intros"
                    :key="lang.lang"
                    type="button"
                    :class="
                      cn(
                        'rounded-full px-2.5 py-1 text-xs font-medium transition-colors',
                        introLang === lang.lang
                          ? 'bg-primary text-white'
                          : 'bg-default-100 text-default-500 hover:bg-default-200'
                      )
                    "
                    @click="introLang = lang.lang"
                  >
                    {{ lang.label }}
                  </button>
                </div>
              </div>
              <p
                class="text-default-700 text-sm leading-relaxed whitespace-pre-wrap"
              >
                {{ currentIntro }}
              </p>
            </KunCard>

            <KunCard
              v-if="galgame.series"
              :is-hoverable="false"
              :is-transparent="false"
              content-class="space-y-2"
            >
              <h3 class="text-foreground text-base font-semibold">所属系列</h3>
              <NuxtLink
                :to="`/series/${galgame.series.id}`"
                class="text-primary inline-flex items-center gap-1 hover:underline"
              >
                {{ galgame.series.name }}
                <Icon name="lucide:external-link" class="size-3.5" />
              </NuxtLink>
              <p
                v-if="galgame.series.description"
                class="text-default-500 text-sm"
              >
                {{ galgame.series.description }}
              </p>
            </KunCard>

            <KunCard
              :is-hoverable="false"
              :is-transparent="false"
              content-class="grid grid-cols-2 gap-3 text-sm"
            >
              <div>
                <p class="text-default-400 text-xs">创建</p>
                <p class="text-foreground">
                  {{
                    new Date(galgame.created_at).toLocaleString('zh-CN')
                  }}
                </p>
              </div>
              <div>
                <p class="text-default-400 text-xs">更新</p>
                <p class="text-foreground">
                  {{
                    new Date(galgame.updated_at).toLocaleString('zh-CN')
                  }}
                </p>
              </div>
            </KunCard>
          </div>

          <!-- ============ Tab: 标签 ============ -->
          <KunCard
            v-else-if="activeTab === 'tags'"
            key="tags"
            :is-hoverable="false"
            :is-transparent="false"
            content-class="space-y-4"
          >
            <div
              v-if="!galgame.tag || galgame.tag.length === 0"
              class="text-default-400 py-10 text-center text-sm"
            >
              暂无标签
            </div>
            <div v-else class="space-y-4">
              <div
                v-for="(tags, cat) in tagsByCategory"
                :key="cat"
                class="space-y-2"
              >
                <div class="flex items-center gap-2">
                  <p class="text-default-500 text-sm font-medium">
                    {{ TAG_CATEGORY_MAP[cat]?.label ?? cat }}
                  </p>
                  <span class="text-default-400 text-xs">
                    {{ tags.length }}
                  </span>
                </div>
                <div class="flex flex-wrap gap-1.5">
                  <span
                    v-for="t in tags"
                    :key="t.id"
                    class="border-default-200 inline-flex items-center gap-1 rounded-full border px-2.5 py-1 text-xs"
                    :class="{
                      'bg-primary-50 text-primary border-primary-200':
                        TAG_CATEGORY_MAP[cat]?.color === 'primary',
                      'bg-secondary-50 text-secondary border-secondary-200':
                        TAG_CATEGORY_MAP[cat]?.color === 'secondary',
                      'bg-info-50 text-info border-info-200':
                        TAG_CATEGORY_MAP[cat]?.color === 'info'
                    }"
                  >
                    {{ t.name }}
                    <span
                      v-if="t.spoiler > 0"
                      class="text-warning-600 font-medium"
                    >
                      ⚠ {{ t.spoiler }}
                    </span>
                  </span>
                </div>
              </div>
            </div>
          </KunCard>

          <!-- ============ Tab: 会社 ============ -->
          <KunCard
            v-else-if="activeTab === 'officials'"
            key="officials"
            :is-hoverable="false"
            :is-transparent="false"
            content-class="space-y-2"
          >
            <div
              v-if="officials.length === 0"
              class="text-default-400 py-10 text-center text-sm"
            >
              暂无关联会社
            </div>
            <div
              v-for="o in officials"
              :key="o.id"
              class="border-default-200 hover:border-primary-300 group flex items-center justify-between rounded-xl border p-4 transition-colors"
            >
              <div class="min-w-0 flex-1 space-y-1">
                <p class="text-foreground font-semibold">{{ o.name }}</p>
                <p
                  v-if="o.original && o.original !== o.name"
                  class="text-default-500 text-sm"
                >
                  {{ o.original }}
                </p>
              </div>
              <div class="flex shrink-0 items-center gap-2 text-xs">
                <span class="text-default-400">{{ o.lang }}</span>
                <span
                  class="rounded-md px-2 py-0.5 font-medium"
                  :class="{
                    'bg-primary-50 text-primary':
                      OFFICIAL_CATEGORY_MAP[o.category]?.color === 'primary',
                    'bg-info-50 text-info':
                      OFFICIAL_CATEGORY_MAP[o.category]?.color === 'info',
                    'bg-default-100 text-default-500':
                      OFFICIAL_CATEGORY_MAP[o.category]?.color === 'default'
                  }"
                >
                  {{
                    OFFICIAL_CATEGORY_MAP[o.category]?.label ?? o.category
                  }}
                </span>
              </div>
            </div>
          </KunCard>

          <!-- ============ Tab: 别名 ============ -->
          <div v-else-if="activeTab === 'aliases'" key="aliases" class="space-y-4">
            <KunCard
              v-if="galgame.alias && galgame.alias.length > 0"
              :is-hoverable="false"
              :is-transparent="false"
              content-class="space-y-2"
            >
              <h3 class="text-foreground text-base font-semibold">
                现有别名
              </h3>
              <div class="flex flex-wrap gap-2">
                <span
                  v-for="a in galgame.alias"
                  :key="a.id"
                  class="bg-default-100 text-default-700 inline-flex items-center rounded-lg px-3 py-1.5 text-sm"
                >
                  {{ a.name }}
                </span>
              </div>
            </KunCard>
            <GalgameAliasesSection :galgame-id="galgame.id" />
          </div>

          <!-- ============ Tab: 链接 ============ -->
          <div v-else-if="activeTab === 'links'" key="links">
            <GalgameLinksSection :galgame-id="galgame.id" />
          </div>

          <!-- ============ Tab: 贡献者 ============ -->
          <div v-else-if="activeTab === 'contributors'" key="contributors">
            <GalgameContributorsSection :galgame-id="galgame.id" />
          </div>
        </Transition>
      </div>
    </div>

    <!-- Edit modal stays at root level so it can open over any tab -->
    <GalgameEditModal
      v-if="galgame"
      :open="editOpen"
      :galgame="galgame"
      @close="editOpen = false"
      @saved="
        () => {
          editOpen = false
          refresh()
        }
      "
    />
  </div>
</template>
