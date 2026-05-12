<script setup lang="ts">
// Avatar upload modal for the user admin table.
//
// Posts a multipart file to `POST /api/v1/admin/users/:uuid/avatar` which
// calls image_service via the SDK and writes back `users.avatar_image_hash`.
//
// On success emits `success` with the new hash so the parent can refresh
// the user list (KunAvatar will then show the new image via the resolver).

interface Props {
  open: boolean
  user: { uuid: string; name: string } | null
}
interface Emits {
  (e: 'update:open', v: boolean): void
  (e: 'success', hash: string): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const file = ref<File | null>(null)
const uploading = ref(false)
const errorMsg = ref('')
const previewUrl = ref('')

const accept = 'image/jpeg,image/png,image/webp'
const maxBytes = 10 * 1024 * 1024

const onPick = (event: Event) => {
  errorMsg.value = ''
  const f = (event.target as HTMLInputElement).files?.[0]
  if (!f) {
    file.value = null
    previewUrl.value = ''
    return
  }
  if (f.size > maxBytes) {
    errorMsg.value = `文件超过 ${(maxBytes / 1024 / 1024).toFixed(0)} MB 限制`
    return
  }
  file.value = f
  previewUrl.value = URL.createObjectURL(f)
}

const close = () => {
  if (uploading.value) return
  emit('update:open', false)
  setTimeout(() => {
    file.value = null
    previewUrl.value = ''
    errorMsg.value = ''
  }, 200)
}

const submit = async () => {
  if (!file.value || !props.user || uploading.value) return
  uploading.value = true
  errorMsg.value = ''

  try {
    const fd = new FormData()
    fd.append('file', file.value)

    const cfg = useRuntimeConfig()
    const cookie = useCookie('access_token')
    const res = await $fetch<{
      code: number
      message: string
      data?: { hash: string }
    }>(`${cfg.public.apiBase}/admin/users/${props.user.uuid}/avatar`, {
      method: 'POST',
      body: fd,
      headers: cookie.value ? { Authorization: `Bearer ${cookie.value}` } : {},
      credentials: 'include'
    })

    if (res.code === 0 && res.data?.hash) {
      emit('success', res.data.hash)
      close()
    } else {
      errorMsg.value = res.message || '上传失败'
    }
  } catch (err) {
    const e = err as { data?: { message?: string }; statusMessage?: string; message?: string }
    errorMsg.value = e?.data?.message || e?.statusMessage || e?.message || '网络错误'
  } finally {
    uploading.value = false
  }
}
</script>

<template>
  <KunModal :modal-value="open" @update:modal-value="close">
    <!--
      KunModal 只有一个默认 slot — `#header` / `#footer` 不存在。
      所有内容（标题、表单、按钮）都按从上到下的顺序放进这一个 slot。
    -->
    <div class="w-[28rem] space-y-4 p-2">
      <h2 class="text-foreground text-lg font-semibold">
        上传头像 — {{ user?.name }}
      </h2>

      <div class="space-y-3">
        <p class="text-default-500 text-sm">
          会推送到 image_service，自动生成 <code>_256</code> / <code>_100</code>
          变体并写入 <code>avatar_image_hash</code>。原 <code>avatar</code> URL
          保留作回退（老用户头像不会被覆盖）。
        </p>

        <input
          type="file"
          :accept="accept"
          class="text-default-500 file:bg-content2 file:text-foreground hover:file:bg-content3 block w-full text-sm file:mr-3 file:rounded file:border-0 file:px-3 file:py-1.5 file:text-sm file:font-medium"
          @change="onPick"
        />

        <div
          v-if="previewUrl"
          class="bg-content2 flex justify-center overflow-hidden rounded p-2"
        >
          <img :src="previewUrl" class="max-h-64 object-contain" alt="预览" />
        </div>

        <div v-if="errorMsg" class="text-danger-600 text-sm">
          {{ errorMsg }}
        </div>
      </div>

      <div class="flex justify-end gap-2 pt-2">
        <KunButton variant="flat" :disabled="uploading" @click="close">
          取消
        </KunButton>
        <KunButton
          color="primary"
          :disabled="!file || uploading"
          @click="submit"
        >
          <Icon
            v-if="uploading"
            name="lucide:loader-2"
            class="mr-1 size-4 animate-spin"
          />
          上传
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>
