<script setup lang="ts">
import type { GalgameTag } from '~/shared/types/galgame'

const props = defineProps<{
  open: boolean
  tag: GalgameTag
  aliases: string[]
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const api = useApi()

const form = ref({
  name: '',
  category: 'content',
  description: '',
  aliasText: ''
})

watch(
  () => props.open,
  (v) => {
    if (v) {
      form.value = {
        name: props.tag.name,
        category: props.tag.category,
        description: props.tag.description ?? '',
        aliasText: props.aliases.join('\n')
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

    const response = await api.put('/tag', {
      tag_id: props.tag.id,
      name: form.value.name,
      category: form.value.category,
      description: form.value.description,
      alias: aliasArr
    })

    if (response.code === 0) {
      useKunMessage('保存成功', 'success')
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
    :modal-value="open"
    inner-class-name="max-w-xl"
    @update:modal-value="(v: boolean) => !v && emit('close')"
  >
    <div class="space-y-4 p-6">
      <h2 class="text-foreground text-lg font-semibold">编辑标签</h2>

      <KunInput v-model="form.name" label="名称" />

      <KunSelect
        v-model="form.category"
        label="分类"
        :options="[
          { value: 'content', label: '内容 (content)' },
          { value: 'sexual', label: '情色 (sexual)' },
          { value: 'technical', label: '技术 (technical)' }
        ]"
      />

      <KunTextarea v-model="form.description" label="描述" :rows="3" />

      <KunTextarea
        v-model="form.aliasText"
        label="别名（每行一个）"
        :rows="4"
      />

      <div class="flex justify-end gap-2 pt-2">
        <KunButton variant="light" @click="emit('close')">取消</KunButton>
        <KunButton
          color="primary"
          :disabled="submitting"
          @click="submit"
        >
          <Icon
            v-if="submitting"
            name="lucide:loader-2"
            class="mr-1 size-4 animate-spin"
          />
          保存
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>
