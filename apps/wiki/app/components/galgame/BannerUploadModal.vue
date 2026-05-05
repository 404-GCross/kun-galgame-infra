<script setup lang="ts">
// Banner upload modal. Posts a multipart file to
// `POST /api/v1/galgame/:gid/banner`, which calls image_service via the SDK
// and writes back `galgame.banner_image_hash`. Emits `success` with the new
// hash so the parent can refresh.

interface Props {
  open: boolean
  galgameId: number
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
const maxBytes = 10 * 1024 * 1024 // server says 10MB unless raised

const onPick = (event: Event) => {
  errorMsg.value = ''
  const input = event.target as HTMLInputElement
  const f = input.files?.[0]
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
  // give the modal animation a tick before clearing state
  setTimeout(() => {
    file.value = null
    previewUrl.value = ''
    errorMsg.value = ''
  }, 200)
}

const submit = async () => {
  if (!file.value || uploading.value) return
  errorMsg.value = ''
  uploading.value = true

  try {
    const fd = new FormData()
    fd.append('file', file.value)

    const cfg = useRuntimeConfig()
    const cookie = useCookie('access_token')
    const res = await $fetch<{
      code: number
      message: string
      data?: { hash: string }
    }>(`${cfg.public.apiBase}/galgame/${props.galgameId}/banner`, {
      method: 'POST',
      body: fd,
      headers: cookie.value ? { Authorization: `Bearer ${cookie.value}` } : {}
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
  <KunModal :model-value="open" @update:model-value="close">
    <template #header>上传 banner</template>

    <div class="space-y-3">
      <p class="text-default-500 text-sm">
        会推送到 image_service，自动生成 <code>_mini</code> 460×259 变体并写入
        <code>banner_image_hash</code>。原 <code>banner</code> URL 保留作回退。
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

    <template #footer>
      <div class="flex justify-end gap-2">
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
    </template>
  </KunModal>
</template>
