<script setup lang="ts">
import type { Galgame } from '~/shared/types/galgame'
import { resolveBannerUrl } from '~/shared/utils/resolveImage'

const props = defineProps<{
  open: boolean
  galgame: Galgame
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const cfg = useRuntimeConfig()
const cdnBase = cfg.public.imageCdnBase as string
const apiBase = cfg.public.apiBase as string
const accessToken = useCookie('wiki_access_token')

interface FormState {
  name_zh_cn: string
  name_ja_jp: string
  name_en_us: string
  name_zh_tw: string
  banner: string                  // legacy URL field; rarely changed manually
  intro_zh_cn: string
  intro_ja_jp: string
  intro_en_us: string
  intro_zh_tw: string
  content_limit: string
  original_language: string
  age_limit: string
  series_id: string
  is_minor: boolean
  mode: 'direct' | 'pr'
  pr_title: string
  pr_message: string
}

const form = ref<FormState>({
  name_zh_cn: '',
  name_ja_jp: '',
  name_en_us: '',
  name_zh_tw: '',
  banner: '',
  intro_zh_cn: '',
  intro_ja_jp: '',
  intro_en_us: '',
  intro_zh_tw: '',
  content_limit: 'sfw',
  original_language: 'ja-jp',
  age_limit: 'r18',
  series_id: '',
  is_minor: false,
  mode: 'direct',
  pr_title: '',
  pr_message: ''
})

// 待上传的 banner 文件。null = 用户没改 banner；选择文件后 = 准备随保存
// 一起 multipart 提交。文件保留在浏览器内存里，未点保存就丢弃 → 不会
// 在 image_service 留下 orphan。
const bannerFile = ref<File | null>(null)
const bannerObjectUrl = ref('')

const { pickFiles: pickBanner, files: pickedBannerFiles } = useFilePicker({
  accept: 'image/jpeg,image/png,image/webp',
  maxSize: 10 * 1024 * 1024,
  onError: (msg) => useKunMessage(msg, 'warn')
})

watch(pickedBannerFiles, ([f]) => {
  if (!f) return
  bannerFile.value = f
  bannerObjectUrl.value = URL.createObjectURL(f)
})

const clearPickedBanner = () => {
  bannerFile.value = null
  bannerObjectUrl.value = ''
}

// 预览：选了新文件 → 本地 blob URL；否则 fallback 到当前持久化状态。
const bannerPreviewUrl = computed(() => {
  if (bannerObjectUrl.value) return bannerObjectUrl.value
  return resolveBannerUrl(
    {
      banner: form.value.banner,
      effective_banner_hash: props.galgame.effective_banner_hash
    },
    { cdnBase }
  )
})

watch(
  () => props.open,
  (v) => {
    if (v) {
      const g = props.galgame
      form.value = {
        name_zh_cn: g.name_zh_cn ?? '',
        name_ja_jp: g.name_ja_jp ?? '',
        name_en_us: g.name_en_us ?? '',
        name_zh_tw: g.name_zh_tw ?? '',
        banner: g.banner ?? '',
        intro_zh_cn: g.intro_zh_cn ?? '',
        intro_ja_jp: g.intro_ja_jp ?? '',
        intro_en_us: g.intro_en_us ?? '',
        intro_zh_tw: g.intro_zh_tw ?? '',
        content_limit: g.content_limit ?? 'sfw',
        original_language: g.original_language ?? 'ja-jp',
        age_limit: g.age_limit ?? 'r18',
        series_id: g.series_id ? String(g.series_id) : '',
        is_minor: false,
        mode: 'direct',
        pr_title: '',
        pr_message: ''
      }
    }
  },
  { immediate: true }
)

const submitting = ref(false)

const submit = async () => {
  submitting.value = true
  try {
    const fieldPayload: Record<string, unknown> = {
      name_zh_cn: form.value.name_zh_cn,
      name_ja_jp: form.value.name_ja_jp,
      name_en_us: form.value.name_en_us,
      name_zh_tw: form.value.name_zh_tw,
      banner: form.value.banner,
      intro_zh_cn: form.value.intro_zh_cn,
      intro_ja_jp: form.value.intro_ja_jp,
      intro_en_us: form.value.intro_en_us,
      intro_zh_tw: form.value.intro_zh_tw,
      content_limit: form.value.content_limit,
      original_language: form.value.original_language,
      age_limit: form.value.age_limit
    }
    if (form.value.series_id) {
      fieldPayload.series_id = Number(form.value.series_id)
    } else {
      fieldPayload.series_id = null
    }

    const path =
      form.value.mode === 'pr'
        ? `/galgame/${props.galgame.id}/prs`
        : `/galgame/${props.galgame.id}`
    const method = form.value.mode === 'pr' ? 'POST' : 'PUT'
    const okMsg = form.value.mode === 'pr' ? 'PR 已提交' : '保存成功'
    const failMsg = form.value.mode === 'pr' ? 'PR 提交失败' : '保存失败'

    const finalPayload =
      form.value.mode === 'pr'
        ? {
            ...fieldPayload,
            title: form.value.pr_title,
            message: form.value.pr_message
          }
        : { ...fieldPayload, is_minor: form.value.is_minor }

    // 二选一的请求方式：
    //   - 没选新 banner 文件 → 走 application/json，跟以前完全一样
    //   - 选了新 banner 文件 → 走 multipart：data 字段是 JSON，file 字段是文件
    //     后端会把文件转给 image_service 拿 hash，再把 hash 当作普通字段
    //     一并写进 revision/PR diff
    const headers: Record<string, string> = accessToken.value
      ? { Authorization: `Bearer ${accessToken.value}` }
      : {}

    let body: string | FormData
    if (bannerFile.value) {
      const fd = new FormData()
      fd.append('data', JSON.stringify(finalPayload))
      fd.append('file', bannerFile.value)
      body = fd
    } else {
      body = JSON.stringify(finalPayload)
      headers['Content-Type'] = 'application/json'
    }

    try {
      const response = await $fetch<{ code: number; message: string }>(
        `${apiBase}${path}`,
        { method, body, headers, credentials: 'include' }
      )
      if (response.code === 0) {
        useKunMessage(okMsg, 'success')
        clearPickedBanner()
        emit('saved')
      } else {
        useKunMessage(response.message || failMsg, 'error')
      }
    } catch (err) {
      const e = err as {
        data?: { message?: string }
        statusMessage?: string
        message?: string
      }
      useKunMessage(
        e?.data?.message || e?.statusMessage || e?.message || failMsg,
        'error'
      )
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <KunModal
    :model-value="open"
    inner-class-name="max-w-3xl"
    @update:model-value="(v: boolean) => !v && emit('close')"
  >
    <div class="max-h-[80vh] space-y-4 overflow-y-auto p-6">
      <h2 class="text-foreground text-lg font-semibold">
        编辑 galgame #{{ galgame.id }}
      </h2>

      <div class="grid grid-cols-2 gap-3">
        <KunInput v-model="form.name_zh_cn" label="简体中文" />
        <KunInput v-model="form.name_ja_jp" label="日本語" />
        <KunInput v-model="form.name_en_us" label="English" />
        <KunInput v-model="form.name_zh_tw" label="繁體中文" />
      </div>

      <!-- Banner section: 选了文件→随保存 multipart 提交；没选→不变。 -->
      <div class="space-y-2">
        <div class="text-foreground text-sm font-medium">封面 banner</div>
        <div class="flex items-start gap-3">
          <div
            class="bg-default-100 border-default-200 flex h-32 w-24 shrink-0 items-center justify-center overflow-hidden rounded border"
          >
            <img
              v-if="bannerPreviewUrl"
              :src="bannerPreviewUrl"
              class="size-full object-cover"
              alt="banner 预览"
            />
            <Icon v-else name="lucide:image" class="text-default-300 size-6" />
          </div>
          <div class="flex flex-1 flex-col gap-2">
            <KunButton
              size="sm"
              variant="flat"
              color="primary"
              type="button"
              @click="pickBanner"
            >
              <Icon name="lucide:image-up" class="mr-1 size-4" />
              {{ bannerFile ? '重新选择 banner' : '替换 banner' }}
            </KunButton>
            <KunButton
              v-if="bannerFile"
              size="sm"
              variant="flat"
              color="warning"
              type="button"
              @click="clearPickedBanner"
            >
              <Icon name="lucide:undo-2" class="mr-1 size-4" />
              不上传这张
            </KunButton>
            <p v-if="bannerFile" class="text-default-400 text-xs">
              已选文件 {{ bannerFile.name }}（{{ (bannerFile.size / 1024).toFixed(1) }} KB），
              **点最下方"保存"或"提交 PR"才会真正上传**；不会留下 orphan。
            </p>
            <p v-else class="text-default-400 text-xs">
              选择新文件即可替换 banner，会随其他字段一起进 revision / PR diff。
            </p>
          </div>
        </div>
        <KunInput
          v-model="form.banner"
          label="老 banner URL（fallback / 兼容；建议留空）"
          placeholder="https://..."
        />
      </div>

      <div class="grid grid-cols-3 gap-3">
        <KunSelect
          v-model="form.content_limit"
          label="内容分级"
          :options="[
            { value: 'sfw', label: 'SFW' },
            { value: 'nsfw', label: 'NSFW' }
          ]"
        />
        <KunInput v-model="form.original_language" label="原语言 (ja-jp...)" />
        <KunSelect
          v-model="form.age_limit"
          label="年龄限制"
          :options="[
            { value: 'all', label: 'all' },
            { value: 'r18', label: 'r18' }
          ]"
        />
      </div>

      <KunInput
        v-model="form.series_id"
        label="系列 ID（留空=脱离系列）"
        type="number"
      />

      <details>
        <summary class="text-default-600 cursor-pointer text-sm">
          展开多语言简介
        </summary>
        <div class="mt-3 space-y-3">
          <KunTextarea v-model="form.intro_zh_cn" label="简介 (简体中文)" :rows="3" />
          <KunTextarea v-model="form.intro_ja_jp" label="简介 (日本語)" :rows="3" />
          <KunTextarea v-model="form.intro_en_us" label="简介 (English)" :rows="3" />
          <KunTextarea v-model="form.intro_zh_tw" label="简介 (繁體中文)" :rows="3" />
        </div>
      </details>

      <div class="border-default-200 space-y-3 rounded-lg border p-4">
        <div class="flex gap-2">
          <KunButton
            :variant="form.mode === 'direct' ? 'solid' : 'light'"
            color="primary"
            size="sm"
            @click="form.mode = 'direct'"
          >
            <Icon name="lucide:check" class="mr-1 size-3.5" />
            直接保存
          </KunButton>
          <KunButton
            :variant="form.mode === 'pr' ? 'solid' : 'light'"
            color="primary"
            size="sm"
            @click="form.mode = 'pr'"
          >
            <Icon name="lucide:git-pull-request" class="mr-1 size-3.5" />
            提交 PR
          </KunButton>
        </div>

        <KunCheckBox
          v-if="form.mode === 'direct'"
          v-model="form.is_minor"
          label="标记为小改动（不创建新 revision，需管理员权限）"
        />

        <template v-if="form.mode === 'pr'">
          <KunInput v-model="form.pr_title" label="PR 标题" required />
          <KunTextarea
            v-model="form.pr_message"
            label="变更说明"
            :rows="3"
          />
        </template>
      </div>

      <div class="flex justify-end gap-2 pt-2">
        <KunButton variant="light" @click="emit('close')">取消</KunButton>
        <KunButton color="primary" :disabled="submitting" @click="submit">
          <Icon
            v-if="submitting"
            name="lucide:loader-2"
            class="mr-1 size-4 animate-spin"
          />
          {{ form.mode === 'pr' ? '提交 PR' : '保存' }}
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>
