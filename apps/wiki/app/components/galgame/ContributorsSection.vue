<script setup lang="ts">
import type { ContributorWithUser } from '~/shared/types/galgame'
import { resolveAvatarUrl } from '~/shared/utils/resolveImage'

const props = defineProps<{ galgameId: number }>()

const api = useApi()

// KunAvatar ≥0.3.4 renders the URL as-is; resolve the small (_100) variant
// from the user's image_service hash (falls back to legacy avatar / sticker).
const cdnBase = useRuntimeConfig().public.imageCdnBase as string
const avatarOf = (
  user?: { avatar: string; avatar_image_hash?: string | null }
) => resolveAvatarUrl(user ?? null, { cdnBase, variant: '100' }, '')
const items = ref<ContributorWithUser[]>([])
const loading = ref(false)

const load = async () => {
  loading.value = true
  try {
    const response = await api.get<ContributorWithUser[]>(
      `/galgame/${props.galgameId}/contributors`
    )
    if (response.code === 0) items.value = response.data ?? []
  } finally {
    loading.value = false
  }
}

const remove = async (userId: number, name: string) => {
  if (
    !(await useKunConfirm({
      title: '移除贡献者',
      content: `确认移除贡献者「${name}」？`,
      confirmText: '移除',
      danger: true
    }))
  )
    return
  const response = await api.delete(
    `/galgame/${props.galgameId}/contributors/${userId}`
  )
  if (response.code === 0) {
    useKunMessage('移除成功', 'success')
    await load()
  } else {
    useKunMessage(response.message || '移除失败', 'error')
  }
}

onMounted(load)
</script>

<template>
  <KunCard class="p-6">
    <h3 class="text-foreground mb-3 text-base font-semibold">
      贡献者 ({{ items.length }})
    </h3>

    <div v-if="loading && items.length === 0" class="text-default-400 py-4 text-center">
      <KunIcon name="lucide:loader-circle" class="inline size-5 animate-spin" />
    </div>

    <div
      v-else-if="items.length === 0"
      class="text-default-400 py-4 text-center text-sm"
    >
      暂无贡献者
    </div>

    <ul v-else class="divide-default-200 divide-y">
      <li
        v-for="c in items"
        :key="c.id"
        class="flex items-center justify-between gap-2 py-2"
      >
        <NuxtLink
          :to="`/user/${c.user_id}/stats`"
          class="flex items-center gap-3"
        >
          <KunAvatar
            v-if="c.user"
            :user="{ id: c.user_id, name: c.user.name, avatar: avatarOf(c.user) }"
            size="sm"
            :is-navigation="false"
          />
          <div>
            <p class="text-foreground hover:text-primary text-sm font-medium">
              {{ c.user?.name ?? `id=${c.user_id}` }}
            </p>
            <p class="text-default-400 text-xs">
              加入于 {{ c.created?.slice(0, 10) }}
            </p>
          </div>
        </NuxtLink>
        <KunButton
          variant="light"
          color="danger"
          size="sm"
          is-icon-only
          aria-label="移除贡献者"
          @click="remove(c.user_id, c.user?.name ?? String(c.user_id))"
        >
          <KunIcon name="lucide:user-minus" class="size-4" />
        </KunButton>
      </li>
    </ul>
  </KunCard>
</template>
