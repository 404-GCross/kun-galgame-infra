<script setup lang="ts">
import type { Galgame } from '~/shared/types/galgame'
import { resolveBannerUrl } from '~/shared/utils/resolveImage'

defineProps<{ items: Galgame[] }>()

const cdnBase = useRuntimeConfig().public.imageCdnBase as string
const displayName = (g: Galgame) =>
  g.name_zh_cn || g.name_ja_jp || g.name_en_us || g.name_zh_tw || '(无标题)'
const coverUrl = (g: Galgame) => resolveBannerUrl(g, { cdnBase, variant: 'mini' })
</script>

<template>
  <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
    <NuxtLink
      v-for="g in items"
      :key="g.id"
      :to="`/galgame/${g.id}`"
      class="group block"
    >
      <div class="bg-default-100 relative aspect-[3/4] overflow-hidden rounded-lg">
        <img
          v-if="coverUrl(g)"
          :src="coverUrl(g)"
          :alt="displayName(g)"
          loading="lazy"
          class="size-full object-cover transition-transform duration-200 group-hover:scale-105"
        />
        <div
          v-else
          class="text-default-300 flex size-full items-center justify-center"
        >
          <KunIcon name="lucide:image" class="size-6" />
        </div>
        <span
          v-if="g.content_limit === 'nsfw'"
          class="bg-danger-50 text-danger absolute right-1 top-1 rounded px-1 text-[10px] font-semibold"
        >
          R18
        </span>
      </div>
      <div
        class="text-foreground group-hover:text-primary mt-1 line-clamp-2 text-sm"
        :title="displayName(g)"
      >
        {{ displayName(g) }}
      </div>
    </NuxtLink>
  </div>
</template>
