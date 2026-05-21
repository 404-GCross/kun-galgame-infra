<script setup lang="ts">
// One row per pending message. Each row carries: actor (who triggered),
// galgame brief, type-specific badge, created_at. Click goes to /review/:gid.
import type { ReviewMessage } from '~/shared/types/review'
import { resolveBannerUrl } from '~/shared/utils/resolveImage'

defineProps<{
  items: ReviewMessage[]
  loading: boolean
}>()

const cdnBase = useRuntimeConfig().public.imageCdnBase as string

const displayName = (m: ReviewMessage) => {
  const g = m.galgame
  if (!g) return '(已删除)'
  return g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw || '(无标题)'
}

const bannerUrl = (m: ReviewMessage) => {
  if (!m.galgame) return ''
  return resolveBannerUrl(
    {
      banner: m.galgame.banner,
      effective_banner_hash: m.galgame.effective_banner_hash ?? undefined
    },
    { cdnBase, variant: 'mini' }
  )
}

const typeMeta = (type: string): { label: string; color: string } => {
  switch (type) {
    case 'submitted':
      return { label: '新提交', color: 'primary' }
    case 'edited_pending':
      return { label: '修订', color: 'warning' }
    default:
      return { label: type, color: 'default' }
  }
}

// Relative time helper — keeps the row compact. ISO timestamps come from
// the API; converted to "刚刚 / N 分钟前 / N 小时前 / N 天前".
const relative = (iso: string) => {
  const t = new Date(iso).getTime()
  const diff = Math.max(0, Date.now() - t) / 1000
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  return `${Math.floor(diff / 86400)} 天前`
}
</script>

<template>
  <div class="bg-content1 border-default-200 overflow-hidden rounded-lg border">
    <div
      v-if="loading"
      class="text-default-400 flex items-center justify-center py-12"
    >
      <Icon name="lucide:loader-2" class="size-6 animate-spin" />
    </div>
    <div
      v-else-if="!items.length"
      class="text-default-400 flex flex-col items-center justify-center py-16"
    >
      <Icon name="lucide:check-check" class="mb-2 size-12 opacity-50" />
      <p>队列已清空</p>
    </div>
    <table v-else class="w-full text-sm">
      <thead class="bg-content2 text-default-500">
        <tr>
          <th class="px-4 py-3 text-left font-medium">提交者</th>
          <th class="px-4 py-3 text-left font-medium">Galgame</th>
          <th class="px-4 py-3 text-left font-medium">类型</th>
          <th class="px-4 py-3 text-left font-medium">时间</th>
          <th class="px-4 py-3 text-right font-medium">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="m in items"
          :key="m.id"
          class="border-default-200 hover:bg-content2/50 border-t transition-colors"
        >
          <td class="px-4 py-3">
            <div class="flex items-center gap-2">
              <KunAvatar
                v-if="m.actor"
                :user="{ id: m.actor.id, name: m.actor.name, avatar: m.actor.avatar }"
                size="sm"
                :is-navigation="false"
              />
              <span class="text-foreground">
                {{ m.actor?.name ?? '(未知)' }}
              </span>
            </div>
          </td>
          <td class="px-4 py-3">
            <div class="flex items-center gap-3">
              <img
                v-if="bannerUrl(m)"
                :src="bannerUrl(m)"
                :alt="displayName(m)"
                class="border-default-200 size-10 rounded border object-cover"
                @error="
                  (e) => ((e.target as HTMLImageElement).style.display = 'none')
                "
              >
              <div class="flex flex-col">
                <span class="text-foreground font-medium">{{ displayName(m) }}</span>
                <span v-if="m.galgame" class="text-default-400 text-xs">
                  id: {{ m.galgame.id }}
                </span>
              </div>
            </div>
          </td>
          <td class="px-4 py-3">
            <KunChip :color="(typeMeta(m.type).color as any)">
              {{ typeMeta(m.type).label }}
            </KunChip>
          </td>
          <td class="text-default-500 px-4 py-3">{{ relative(m.created_at) }}</td>
          <td class="px-4 py-3 text-right">
            <NuxtLink :to="`/review/${m.galgame_id}`">
              <KunButton variant="light" size="sm">
                <Icon name="lucide:eye" class="mr-1 size-4" />
                查看
              </KunButton>
            </NuxtLink>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
