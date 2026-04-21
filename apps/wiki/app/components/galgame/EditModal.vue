<script setup lang="ts">
import type { Galgame } from '~/shared/types/galgame'

const props = defineProps<{
  open: boolean
  galgame: Galgame
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const api = useApi()

interface FormState {
  name_zh_cn: string
  name_ja_jp: string
  name_en_us: string
  name_zh_tw: string
  banner: string
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

    if (form.value.mode === 'pr') {
      const prPayload: Record<string, unknown> = {
        ...fieldPayload,
        title: form.value.pr_title,
        message: form.value.pr_message
      }
      const response = await api.post(
        `/galgame/${props.galgame.id}/prs`,
        prPayload
      )
      if (response.code === 0) {
        useKunMessage('PR 已提交', 'success')
        emit('saved')
      } else {
        useKunMessage(response.message || 'PR 提交失败', 'error')
      }
    } else {
      const response = await api.put(`/galgame/${props.galgame.id}`, {
        ...fieldPayload,
        is_minor: form.value.is_minor
      })
      if (response.code === 0) {
        useKunMessage('保存成功', 'success')
        emit('saved')
      } else {
        useKunMessage(response.message || '保存失败', 'error')
      }
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <KunModal
    :modal-value="open"
    inner-class-name="max-w-3xl"
    @update:modal-value="(v: boolean) => !v && emit('close')"
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

      <KunInput v-model="form.banner" label="封面图 URL" placeholder="https://..." />

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
