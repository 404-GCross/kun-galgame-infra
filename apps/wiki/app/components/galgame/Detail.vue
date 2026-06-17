<script setup lang="ts">
import {
  GALGAME_STATUS_MAP,
  CONTENT_LIMIT_MAP,
  TAG_CATEGORY_MAP,
  OFFICIAL_CATEGORY_MAP,
  REVIEW_ACTIONS
} from '~/constants/admin'
import type { Galgame } from '~/shared/types/galgame'
import { resolveBannerUrl, imageHashUrl } from '~/shared/utils/resolveImage'
import { formatReleaseDate } from '~/shared/utils/format'
import type { KunUIColor } from '@kungal/ui-core'

const api = useApi()
const route = useRoute()
const router = useRouter()
const cdnBase = useRuntimeConfig().public.imageCdnBase as string

const id = computed(() => Number(route.params.id))
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

// ────────────────────────────────────────────────
// Intro language switcher — KunTab pills variant
// ────────────────────────────────────────────────
const intros = computed(() => {
  const g = galgame.value
  if (!g) return [] as { lang: string; label: string; text: string }[]
  return [
    { lang: 'zh_cn', label: '简体中文', text: g.intro_zh_cn },
    { lang: 'ja_jp', label: '日本語', text: g.intro_ja_jp },
    { lang: 'en_us', label: 'English', text: g.intro_en_us },
    { lang: 'zh_tw', label: '繁體中文', text: g.intro_zh_tw }
  ].filter((i): i is { lang: string; label: string; text: string } =>
    Boolean(i.text)
  )
})

// KunTab requires v-model<string> + items[]. textValue is what's shown
// to the user; value is the key we toggle on.
const introLang = ref<string>('')
const introTabs = computed(() =>
  intros.value.map((i) => ({ value: i.lang, textValue: i.label }))
)
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
// ────────────────────────────────────────────────
// Admin status actions — reuse the canonical ReviewActionModal, which PUTs
// /admin/galgame/:id/status with status 0/1/4 (+ reason). Each action is
// gated to the source states the backend actually accepts, so no click can
// ever hit the `oneof`/transition validation (the old hardcoded 撤回草稿→2
// button always 400'd; 2 was dropped from the contract on 2026-05-12):
//   通过审核 (→0)  any state except already-published
//   拒绝     (→4)  pending(3) only — backend rejects decline from elsewhere
//   封禁     (→1)  any state except already-banned
// ────────────────────────────────────────────────
const actionOpen = ref<'approve' | 'decline' | 'ban' | null>(null)

const availableActions = computed(() => {
  const s = galgame.value?.status
  if (s === undefined || s === null) return []
  return REVIEW_ACTIONS.filter((a) =>
    a.status === 4 ? s === 3 : a.status !== s
  )
})

const currentAction = computed(
  () => REVIEW_ACTIONS.find((a) => a.id === actionOpen.value) ?? null
)

const onActionDone = () => {
  useKunMessage(`${currentAction.value?.label ?? '操作'}成功`, 'success')
  actionOpen.value = null
  refresh()
}

// ────────────────────────────────────────────────
// Page tabs — KunTab underlined, with count inlined in label
// ────────────────────────────────────────────────
type TabId =
  | 'overview'
  | 'gallery'
  | 'tags'
  | 'officials'
  | 'aliases'
  | 'links'
  | 'contributors'

const activeTab = ref<TabId>('overview')

const galleryCount = computed(
  () =>
    (galgame.value?.covers?.length ?? 0) +
    (galgame.value?.screenshots?.length ?? 0)
)

const tabItems = computed(() => [
  {
    value: 'overview',
    textValue: '基本',
    icon: 'lucide:info'
  },
  {
    value: 'gallery',
    textValue: `图集 ${galleryCount.value}`,
    icon: 'lucide:images'
  },
  {
    value: 'tags',
    textValue: `标签 ${galgame.value?.tag?.length ?? 0}`,
    icon: 'lucide:tags'
  },
  {
    value: 'officials',
    textValue: `会社 ${officials.value.length}`,
    icon: 'lucide:building-2'
  },
  {
    value: 'aliases',
    textValue: `别名 ${galgame.value?.alias?.length ?? 0}`,
    icon: 'lucide:type'
  },
  { value: 'links', textValue: '链接', icon: 'lucide:link' },
  { value: 'contributors', textValue: '贡献者', icon: 'lucide:users' }
])

// ────────────────────────────────────────────────
// Color helpers — narrow string lookups to KunUIColor so KunChip props
// type-check. Default to 'default' for unknown keys.
// ────────────────────────────────────────────────
const KUNUI_COLOR_VALUES: ReadonlyArray<KunUIColor> = [
  'default',
  'primary',
  'secondary',
  'success',
  'warning',
  'danger',
  'info'
]
const toKunColor = (raw: string | undefined): KunUIColor => {
  return (KUNUI_COLOR_VALUES as ReadonlyArray<string>).includes(raw ?? '')
    ? (raw as KunUIColor)
    : 'default'
}

const statusColor = computed<KunUIColor>(() =>
  toKunColor(GALGAME_STATUS_MAP[galgame.value?.status ?? 0]?.color)
)
const contentLimitColor = computed<KunUIColor>(() =>
  toKunColor(CONTENT_LIMIT_MAP[galgame.value?.content_limit ?? '']?.color)
)
const tagCategoryColor = (cat: string): KunUIColor =>
  toKunColor(TAG_CATEGORY_MAP[cat]?.color)
const officialCategoryColor = (cat: string): KunUIColor =>
  toKunColor(OFFICIAL_CATEGORY_MAP[cat]?.color)
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
          <KunIcon name="lucide:arrow-left" class="size-5" />
        </KunButton>
        <h1 class="text-foreground text-2xl font-bold">Galgame 详情</h1>
        <span
          v-if="galgame"
          class="text-default-400 font-mono text-sm"
        >
          #{{ galgame.id }}
        </span>
      </div>
      <KunChip
        v-if="galgame"
        :color="statusColor"
        variant="flat"
        size="md"
      >
        <KunIcon name="lucide:circle-dot" class="size-3" />
        {{ GALGAME_STATUS_MAP[galgame.status]?.label }}
      </KunChip>
    </div>

    <div
      v-if="loading && !galgame"
      class="text-default-400 flex items-center justify-center py-20"
    >
      <KunIcon name="lucide:loader-circle" class="size-6 animate-spin" />
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
             stable regardless of image size). KunImage auto-shows
             skeleton (v0.5.0) while loading. -->
        <div
          class="bg-default-100 border-default-200 relative overflow-hidden rounded-xl border"
          style="aspect-ratio: 16 / 9"
        >
          <KunImage
            v-if="bannerSrc"
            :src="bannerSrc"
            :alt="displayName"
            loading="lazy"
            class-name="size-full object-cover"
          />
          <div
            v-else
            class="flex size-full items-center justify-center"
          >
            <KunIcon name="lucide:image" class="text-default-300 size-10" />
          </div>
          <!-- Content-limit chip overlaid on banner top-left -->
          <KunChip
            :color="contentLimitColor"
            variant="solid"
            size="sm"
            class-name="absolute top-2 left-2 shadow-md"
          >
            {{ CONTENT_LIMIT_MAP[galgame.content_limit]?.label }}
          </KunChip>
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
            <KunLink
              :to="`https://vndb.org/${galgame.vndb_id}`"
              target="_blank"
              size="sm"
              underline="hover"
              is-show-anchor-icon
              class-name="font-mono"
            >
              {{ galgame.vndb_id }}
            </KunLink>
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
            <KunChip
              :color="galgame.age_limit === 'r18' ? 'danger' : 'success'"
              variant="flat"
              size="sm"
            >
              {{ galgame.age_limit === 'r18' ? 'R18' : '全年龄' }}
            </KunChip>
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
            <KunIcon name="lucide:pencil" class="mr-1 size-4" />
            编辑
          </KunButton>
          <!-- Wrap KunButton in NuxtLink (block) instead of KunButton's
               :href prop — the latter uses defineNuxtLink({}) inside
               a :is binding which doesn't reliably trigger client-side
               navigation under some Nuxt 4 versions (URL updates, page
               doesn't render). NuxtLink wrap is the canonical pattern
               and matches the original Detail.vue. -->
          <div class="grid grid-cols-2 gap-2">
            <NuxtLink
              :to="`/galgame/${galgame.id}/revisions`"
              class="block"
            >
              <KunButton variant="flat" size="sm" full-width>
                <KunIcon name="lucide:history" class="mr-1 size-4" />
                修订
              </KunButton>
            </NuxtLink>
            <NuxtLink
              :to="`/galgame/${galgame.id}/prs`"
              class="block"
            >
              <KunButton variant="flat" size="sm" full-width>
                <KunIcon
                  name="lucide:git-pull-request"
                  class="mr-1 size-4"
                />
                PR
              </KunButton>
            </NuxtLink>
          </div>
        </div>

        <!-- Status change actions — gated to the transitions the backend
             accepts (status 0/1/4); each opens ReviewActionModal. -->
        <div v-if="availableActions.length" class="space-y-2">
          <KunDivider />
          <p class="text-default-400 text-xs">状态变更</p>
          <div class="flex flex-col gap-2">
            <KunButton
              v-for="action in availableActions"
              :key="action.id"
              :color="(action.color as KunUIColor)"
              variant="flat"
              size="sm"
              full-width
              @click="actionOpen = action.id"
            >
              <KunIcon :name="action.icon" class="mr-1 size-4" />
              {{ action.label }}
            </KunButton>
          </div>
        </div>
      </aside>

      <!-- ============ Tab content area ============ -->
      <div class="space-y-4">
        <!-- Page tab nav — KunTab underlined, sticky under page header -->
        <div
          class="bg-background/85 sticky top-0 z-20 -mx-2 border-default-200 border-b backdrop-blur-md px-2 py-1"
        >
          <KunTab
            v-model="activeTab"
            :items="tabItems"
            variant="underlined"
            color="primary"
            size="md"
            scrollable
          />
        </div>

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
              <div class="flex items-center justify-between gap-3">
                <h3 class="text-foreground text-base font-semibold">简介</h3>
                <!-- Language switcher — KunTab pills variant: compact
                     row of language pills, exactly what we want. -->
                <KunTab
                  v-if="introTabs.length > 1"
                  v-model="introLang"
                  :items="introTabs"
                  variant="pills"
                  color="primary"
                  size="sm"
                />
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
              <KunLink
                :to="`/series/${galgame.series.id}`"
                underline="hover"
                is-show-anchor-icon
              >
                {{ galgame.series.name }}
              </KunLink>
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
                    new Date(galgame.created).toLocaleString('zh-CN')
                  }}
                </p>
              </div>
              <div>
                <p class="text-default-400 text-xs">更新</p>
                <p class="text-foreground">
                  {{
                    new Date(galgame.updated).toLocaleString('zh-CN')
                  }}
                </p>
              </div>
            </KunCard>
          </div>

          <!-- ============ Tab: 图集 ============ -->
          <!-- covers (pinned 封面候选) + screenshots (gallery/CG) merged
               into a single grid + lightbox. Each entry is its own
               KunLightboxGalleryItem so clicking any thumbnail opens the
               viewer at that position with ←/→ flipping through ALL
               entries (covers先, screenshots后, by sort_order).
               -->
          <KunCard
            v-else-if="activeTab === 'gallery'"
            key="gallery"
            :is-hoverable="false"
            :is-transparent="false"
            content-class="space-y-4"
          >
            <div
              v-if="galleryCount === 0"
              class="text-default-400 py-10 text-center text-sm"
            >
              暂无封面 / 截图
            </div>
            <KunLightboxGallery v-else>
              <div
                v-if="galgame.covers && galgame.covers.length"
                class="space-y-2"
              >
                <p class="text-default-500 text-sm font-medium">
                  封面候选 ({{ galgame.covers.length }})
                </p>
                <div
                  class="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4"
                >
                  <KunLightboxGalleryItem
                    v-for="c in [...galgame.covers].sort((a, b) => a.sort_order - b.sort_order)"
                    :key="`cover-${c.image_hash}`"
                    :src="imageHashUrl(c.image_hash, { cdnBase })"
                    :alt="`封面 sort_order=${c.sort_order}`"
                    as="div"
                  >
                    <div
                      class="bg-default-100 border-default-200 relative overflow-hidden rounded-lg border"
                      style="aspect-ratio: 16 / 9"
                    >
                      <KunImage
                        :src="imageHashUrl(c.image_hash, { cdnBase, variant: 'mini' })"
                        :alt="`封面 ${c.sort_order}`"
                        loading="lazy"
                        class-name="size-full object-cover"
                      />
                      <!-- sort_order=0 = pinned (= effective banner). -->
                      <KunChip
                        v-if="c.sort_order === 0"
                        color="primary"
                        variant="solid"
                        size="xs"
                        class-name="absolute top-1.5 left-1.5 shadow-md"
                      >
                        当前 banner
                      </KunChip>
                    </div>
                  </KunLightboxGalleryItem>
                </div>
              </div>

              <div
                v-if="galgame.screenshots && galgame.screenshots.length"
                class="space-y-2"
              >
                <p class="text-default-500 text-sm font-medium">
                  截图 / CG ({{ galgame.screenshots.length }})
                </p>
                <div
                  class="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4"
                >
                  <KunLightboxGalleryItem
                    v-for="s in [...galgame.screenshots].sort((a, b) => a.sort_order - b.sort_order)"
                    :key="`shot-${s.image_hash}`"
                    :src="imageHashUrl(s.image_hash, { cdnBase })"
                    :alt="s.caption || `截图 ${s.sort_order}`"
                    as="div"
                  >
                    <div
                      class="bg-default-100 border-default-200 relative overflow-hidden rounded-lg border"
                      style="aspect-ratio: 16 / 9"
                    >
                      <KunImage
                        :src="imageHashUrl(s.image_hash, { cdnBase, variant: 'mini' })"
                        :alt="s.caption || `截图 ${s.sort_order}`"
                        loading="lazy"
                        class-name="size-full object-cover"
                      />
                    </div>
                    <p
                      v-if="s.caption"
                      class="text-default-500 mt-1 text-xs line-clamp-2"
                    >
                      {{ s.caption }}
                    </p>
                  </KunLightboxGalleryItem>
                </div>
              </div>
            </KunLightboxGallery>
          </KunCard>

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
                  <!-- Each tag chip links to its detail page so admins
                       can jump straight to /tag/:id from here. -->
                  <NuxtLink
                    v-for="t in tags"
                    :key="t.id"
                    :to="`/tag/${t.id}`"
                  >
                    <KunChip
                      :color="tagCategoryColor(String(cat))"
                      variant="flat"
                      size="sm"
                      class-name="cursor-pointer hover:opacity-80 transition-opacity"
                    >
                      {{ t.name }}
                      <span
                        v-if="t.spoiler > 0"
                        class="text-warning-600 ml-1 font-medium"
                      >
                        ⚠ {{ t.spoiler }}
                      </span>
                    </KunChip>
                  </NuxtLink>
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
            <!-- Each row is a NuxtLink to /official/:id so admins can
                 jump to the official's detail page (where its other
                 galgames + edit are exposed). Wrap is `block` so the
                 inner flex row keeps its geometry. -->
            <NuxtLink
              v-for="o in officials"
              :key="o.id"
              :to="`/official/${o.id}`"
              class="block"
            >
              <div
                class="border-default-200 hover:border-primary-300 hover:bg-primary-50/30 group flex items-center justify-between rounded-xl border p-4 transition-colors"
              >
                <div class="min-w-0 flex-1 space-y-1">
                  <p
                    class="text-foreground group-hover:text-primary font-semibold transition-colors"
                  >
                    {{ o.name }}
                  </p>
                  <p
                    v-if="o.original && o.original !== o.name"
                    class="text-default-500 text-sm"
                  >
                    {{ o.original }}
                  </p>
                </div>
                <div class="flex shrink-0 items-center gap-2 text-xs">
                  <span class="text-default-400">{{ o.lang }}</span>
                  <KunChip
                    :color="officialCategoryColor(o.category)"
                    variant="flat"
                    size="sm"
                  >
                    {{
                      OFFICIAL_CATEGORY_MAP[o.category]?.label ?? o.category
                    }}
                  </KunChip>
                  <KunIcon
                    name="lucide:chevron-right"
                    class="text-default-300 group-hover:text-primary size-4 transition-colors"
                  />
                </div>
              </div>
            </NuxtLink>
          </KunCard>

          <!-- ============ Tab: 别名 ============ -->
          <div v-else-if="activeTab === 'aliases'" key="aliases" class="space-y-4">
            <KunCard
              v-if="galgame.alias && galgame.alias.length > 0"
              :is-hoverable="false"
              :is-transparent="false"
              content-class="space-y-3"
            >
              <h3 class="text-foreground text-base font-semibold">
                现有别名
              </h3>
              <div class="flex flex-wrap gap-2">
                <KunChip
                  v-for="a in galgame.alias"
                  :key="a.id"
                  variant="flat"
                  color="default"
                  size="md"
                >
                  {{ a.name }}
                </KunChip>
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

    <!-- Admin status-change modal (approve / decline / ban + reason) -->
    <ReviewActionModal
      v-if="currentAction && galgame"
      :open="!!currentAction"
      :galgame-id="galgame.id"
      :action="currentAction"
      @close="actionOpen = null"
      @done="onActionDone"
    />
  </div>
</template>
