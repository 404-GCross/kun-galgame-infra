<script setup lang="ts">
import type { GalgameOfficial } from '~/shared/types/galgame'

// Used for both create (official=null) and edit (official=given).
const props = defineProps<{
  open: boolean
  official?: GalgameOfficial | null
  aliases?: string[]
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const api = useApi()

const isEdit = computed(() => !!props.official?.id)

const form = ref({
  name: '',
  link: '',
  category: 'company',
  lang: '',
  description: '',
  aliasText: ''
})

watch(
  () => props.open,
  (v) => {
    if (v) {
      form.value = {
        name: props.official?.name ?? '',
        link: props.official?.link ?? '',
        category: props.official?.category ?? 'company',
        lang: props.official?.lang ?? '',
        description: props.official?.description ?? '',
        aliasText: (props.aliases ?? []).join('\n')
      }
    }
  },
  { immediate: true }
)

const submitting = ref(false)

const submit = async () => {
  submitting.value = true
  try {
    const aliasArr = form.value.aliasText
      .split('\n')
      .map((s) => s.trim())
      .filter(Boolean)

    const response = isEdit.value
      ? await api.put('/official', {
          official_id: props.official!.id,
          name: form.value.name,
          link: form.value.link,
          category: form.value.category,
          lang: form.value.lang,
          description: form.value.description,
          alias: aliasArr
        })
      : await api.post('/official', {
          name: form.value.name,
          link: form.value.link,
          category: form.value.category,
          lang: form.value.lang,
          description: form.value.description,
          alias: aliasArr
        })

    if (response.code === 0) {
      useKunMessage(isEdit.value ? '保存成功' : '创建成功', 'success')
      emit('saved')
    } else {
      useKunMessage(response.message || '保存失败', 'error')
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <KunModal
    :model-value="open"
    inner-class-name="max-w-xl"
    @update:model-value="(v: boolean) => !v && emit('close')"
  >
    <div class="space-y-4 p-6">
      <h2 class="text-foreground text-lg font-semibold">
        {{ isEdit ? '编辑会社' : '创建会社' }}
      </h2>

      <KunInput v-model="form.name" label="名称" />
      <KunInput v-model="form.link" label="官网链接" placeholder="https://..." />

      <div class="grid grid-cols-2 gap-3">
        <KunSelect
          v-model="form.category"
          label="类型"
          :options="[
            { value: 'company', label: '公司 (company)' },
            { value: 'individual', label: '个人 (individual)' },
            { value: 'amateur', label: '同人 (amateur)' }
          ]"
        />
        <KunInput v-model="form.lang" label="语言 (ja/en/zh-Hans ...)" />
      </div>

      <KunTextarea v-model="form.description" label="描述" :rows="3" />
      <KunTextarea v-model="form.aliasText" label="别名（每行一个）" :rows="4" />

      <div class="flex justify-end gap-2 pt-2">
        <KunButton variant="light" @click="emit('close')">取消</KunButton>
        <KunButton color="primary" :disabled="submitting" @click="submit">
          <KunIcon
            v-if="submitting"
            name="lucide:loader-circle"
            class="mr-1 size-4 animate-spin"
          />
          {{ isEdit ? '保存' : '创建' }}
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>
