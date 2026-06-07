<script setup lang="ts">
// Self-service profile editor: name / bio / avatar URL via PATCH /auth/me
// (useAuth.updateProfile). Email & password are intentionally NOT here —
// they have dedicated verified flows (ProfileEmailChange /
// ProfilePasswordChange). Avatar is a URL field: the OAuth server has no
// self upload endpoint (the image_service-backed upload is admin-only,
// POST /admin/users/:uuid/avatar), so self users set an avatar URL; the
// server also accepts avatar_image_hash if a hash is ever obtained.

const auth = useAuth()
const user = auth.user

const name = ref('')
const bio = ref('')
const avatar = ref('')
const error = ref('')
const success = ref('')
const isLoading = ref(false)

// Prefill from the store, and re-sync if the user object changes
// (e.g. after fetchUser / a successful save elsewhere).
watchEffect(() => {
  if (!user.value) return
  name.value = user.value.name ?? ''
  bio.value = user.value.bio ?? ''
  avatar.value = user.value.avatar ?? ''
})

const dirty = computed(
  () =>
    !!user.value &&
    (name.value !== (user.value.name ?? '') ||
      bio.value !== (user.value.bio ?? '') ||
      avatar.value !== (user.value.avatar ?? ''))
)

const handleSubmit = async () => {
  error.value = ''
  success.value = ''

  if (name.value.trim().length < 2 || name.value.trim().length > 17) {
    error.value = '用户名长度需为 2–17 个字符'
    return
  }
  if (bio.value.length > 107) {
    error.value = '个人简介不能超过 107 个字符'
    return
  }
  if (avatar.value.length > 255) {
    error.value = '头像链接过长（≤255）'
    return
  }
  if (!dirty.value) {
    error.value = '没有任何改动'
    return
  }

  // Only send changed keys. PATCH /auth/me has pointer semantics: a key
  // present with "" CLEARS that field — sending an unchanged empty avatar
  // would wipe it, so we diff against the current user first.
  const payload: { name?: string; bio?: string; avatar?: string } = {}
  if (name.value !== (user.value?.name ?? '')) payload.name = name.value.trim()
  if (bio.value !== (user.value?.bio ?? '')) payload.bio = bio.value
  if (avatar.value !== (user.value?.avatar ?? '')) payload.avatar = avatar.value

  isLoading.value = true
  try {
    const response = await auth.updateProfile(payload)
    if (response.code === 0) {
      success.value = '资料已更新'
    } else {
      error.value = response.message || '更新失败'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '更新失败'
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunCard class="p-6">
    <h3 class="mb-4 text-lg font-semibold text-foreground">
      <KunIcon name="lucide:user-pen" class="mr-2 inline size-5" />
      编辑资料
    </h3>

    <form class="space-y-4" @submit.prevent="handleSubmit">
      <div class="flex items-center gap-4">
        <KunAvatar
          :user="{ id: 0, name: name || '用户', avatar }"
          size="lg"
          :is-navigation="false"
        />
        <div class="flex-1">
          <KunInput
            v-model="avatar"
            label="头像链接"
            placeholder="https://example.com/avatar.webp"
            autocomplete="off"
          />
        </div>
      </div>

      <KunInput
        v-model="name"
        label="用户名"
        placeholder="2–17 个字符，全站唯一"
        required
        autocomplete="username"
      />

      <KunTextarea
        v-model="bio"
        label="个人简介"
        placeholder="一句话介绍自己（≤107 字）"
        :rows="3"
      />

      <div v-if="error" class="rounded-lg bg-danger-50 p-3 text-sm text-danger">
        {{ error }}
      </div>

      <div v-if="success" class="rounded-lg bg-success-50 p-3 text-sm text-success">
        {{ success }}
      </div>

      <KunButton
        type="submit"
        color="primary"
        class="w-full"
        :disabled="isLoading || !dirty"
      >
        <KunIcon
          v-if="isLoading"
          name="lucide:loader-circle"
          class="mr-2 size-4 animate-spin"
        />
        {{ isLoading ? '保存中...' : '保存修改' }}
      </KunButton>
    </form>
  </KunCard>
</template>
