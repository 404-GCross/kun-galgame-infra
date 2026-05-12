<script setup lang="ts">
const api = useApi()
const router = useRouter()
const cfg = useRuntimeConfig()
const apiBase = cfg.public.apiBase as string
const accessToken = useCookie('wiki_access_token')

const form = ref({
  vndb_id: '',
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
  aliases: ''
})

// 待上传的 banner 文件（与 EditModal 一致：浏览器内存暂存，跟着 multipart 一起提交）
const bannerFile = ref<File | null>(null)
const bannerObjectUrl = ref('')
const bannerInputRef = ref<HTMLInputElement | null>(null)

const onPickBanner = (event: Event) => {
  const f = (event.target as HTMLInputElement).files?.[0] ?? null
  if (!f) {
    bannerFile.value = null
    bannerObjectUrl.value = ''
    return
  }
  if (f.size > 10 * 1024 * 1024) {
    useKunMessage('文件超过 10MB 上限', 'warn')
    return
  }
  bannerFile.value = f
  bannerObjectUrl.value = URL.createObjectURL(f)
}

const clearPickedBanner = () => {
  bannerFile.value = null
  bannerObjectUrl.value = ''
  if (bannerInputRef.value) bannerInputRef.value.value = ''
}

const vndbCheck = ref<{
  status: 'idle' | 'checking' | 'ok' | 'exists'
  galgameId?: number
}>({ status: 'idle' })

let checkTimer: ReturnType<typeof setTimeout> | null = null
const checkVNDB = async () => {
  const id = form.value.vndb_id.trim()
  if (!id) {
    vndbCheck.value = { status: 'idle' }
    return
  }
  vndbCheck.value = { status: 'checking' }
  const response = await api.get<{ exists: boolean; galgame_id?: number }>(
    '/galgame/check',
    { vndb_id: id }
  )
  if (response.code === 0) {
    if (response.data.exists) {
      vndbCheck.value = { status: 'exists', galgameId: response.data.galgame_id }
    } else {
      vndbCheck.value = { status: 'ok' }
    }
  }
}

watch(
  () => form.value.vndb_id,
  () => {
    if (checkTimer) clearTimeout(checkTimer)
    checkTimer = setTimeout(checkVNDB, 400)
  }
)

const submitting = ref(false)

const canSubmit = computed(() => {
  const hasName =
    form.value.name_ja_jp || form.value.name_en_us || form.value.name_zh_cn
  return (
    !!form.value.vndb_id.trim() &&
    !!hasName &&
    vndbCheck.value.status === 'ok' &&
    !submitting.value
  )
})

const submit = async () => {
  submitting.value = true
  try {
    const payload: Record<string, unknown> = {
      vndb_id: form.value.vndb_id.trim(),
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
      age_limit: form.value.age_limit,
      aliases: form.value.aliases
    }

    // 选了文件 → multipart；没选 → 老 JSON 路径
    const headers: Record<string, string> = accessToken.value
      ? { Authorization: `Bearer ${accessToken.value}` }
      : {}

    let body: string | FormData
    if (bannerFile.value) {
      const fd = new FormData()
      fd.append('data', JSON.stringify(payload))
      fd.append('file', bannerFile.value)
      body = fd
    } else {
      body = JSON.stringify(payload)
      headers['Content-Type'] = 'application/json'
    }

    try {
      const response = await $fetch<{
        code: number
        message: string
        data?: { id: number }
      }>(`${apiBase}/galgame`, {
        method: 'POST',
        body,
        headers,
        credentials: 'include'
      })
      if (response.code === 0) {
        useKunMessage('创建成功', 'success')
        if (response.data?.id) {
          router.push(`/galgame/${response.data.id}`)
        } else {
          router.push('/galgame')
        }
      } else {
        useKunMessage(response.message || '创建失败', 'error')
      }
    } catch (err) {
      const e = err as {
        data?: { message?: string }
        statusMessage?: string
        message?: string
      }
      useKunMessage(
        e?.data?.message || e?.statusMessage || e?.message || '创建失败',
        'error'
      )
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="max-w-3xl space-y-4">
    <div class="flex items-center gap-3">
      <button
        class="text-default-500 hover:bg-default-100 rounded-lg p-2 transition-colors"
        @click="router.back()"
      >
        <Icon name="lucide:arrow-left" class="size-5" />
      </button>
      <h1 class="text-foreground text-2xl font-bold">新建 galgame</h1>
    </div>

    <KunCard class="space-y-4 p-6">
      <div>
        <KunInput
          v-model="form.vndb_id"
          label="VNDB ID"
          placeholder="v17"
          required
        />
        <p class="mt-1 text-xs">
          <span
            v-if="vndbCheck.status === 'checking'"
            class="text-default-400"
          >
            <Icon name="lucide:loader-2" class="inline size-3 animate-spin" />
            校验中...
          </span>
          <span v-else-if="vndbCheck.status === 'ok'" class="text-success">
            <Icon name="lucide:check" class="inline size-3" />
            可用
          </span>
          <span v-else-if="vndbCheck.status === 'exists'" class="text-danger">
            <Icon name="lucide:alert-circle" class="inline size-3" />
            已存在
            <NuxtLink
              v-if="vndbCheck.galgameId"
              :to="`/galgame/${vndbCheck.galgameId}`"
              class="ml-1 hover:underline"
            >
              (查看 #{{ vndbCheck.galgameId }})
            </NuxtLink>
          </span>
        </p>
      </div>

      <div class="grid grid-cols-2 gap-3">
        <KunInput v-model="form.name_zh_cn" label="简体中文" />
        <KunInput v-model="form.name_ja_jp" label="日本語" />
        <KunInput v-model="form.name_en_us" label="English" />
        <KunInput v-model="form.name_zh_tw" label="繁體中文" />
      </div>

      <!-- Banner: 选了文件→随提交 multipart 上传；填 URL→老路径兼容 -->
      <div class="space-y-2">
        <div class="text-foreground text-sm font-medium">封面 banner</div>
        <div class="flex items-start gap-3">
          <div
            class="bg-default-100 border-default-200 flex h-32 w-24 shrink-0 items-center justify-center overflow-hidden rounded border"
          >
            <img
              v-if="bannerObjectUrl"
              :src="bannerObjectUrl"
              class="size-full object-cover"
              alt="banner 预览"
            />
            <Icon v-else name="lucide:image" class="text-default-300 size-6" />
          </div>
          <div class="flex flex-1 flex-col gap-2">
            <input
              ref="bannerInputRef"
              type="file"
              accept="image/jpeg,image/png,image/webp"
              class="text-default-500 file:bg-content2 file:text-foreground hover:file:bg-content3 block w-full text-sm file:mr-3 file:rounded file:border-0 file:px-3 file:py-1.5 file:text-sm file:font-medium"
              @change="onPickBanner"
            />
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
              已选 {{ bannerFile.name }}（{{ (bannerFile.size / 1024).toFixed(1) }} KB），
              **点最下方"创建"才会真正上传**。
            </p>
          </div>
        </div>
        <KunInput
          v-model="form.banner"
          label="或填 banner URL（老路径兼容）"
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

      <KunTextarea
        v-model="form.aliases"
        label="别名（逗号分隔）"
        :rows="2"
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

      <div class="flex justify-end gap-2">
        <KunButton variant="light" @click="router.back()">取消</KunButton>
        <KunButton color="primary" :disabled="!canSubmit" @click="submit">
          <Icon
            v-if="submitting"
            name="lucide:loader-2"
            class="mr-1 size-4 animate-spin"
          />
          创建
        </KunButton>
      </div>
    </KunCard>
  </div>
</template>
