<script setup lang="ts">
import type { GalgameEngine } from '~/shared/types/galgame'

// Used for both create (engine=null) and edit (engine=given).
const props = defineProps<{
  open: boolean
  engine?: GalgameEngine | null
  aliases?: string[]
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const api = useApi()

const isEdit = computed(() => !!props.engine?.id)

const form = ref({
  name: '',
  description: '',
  aliasText: ''
})

watch(
  () => props.open,
  (v) => {
    if (v) {
      form.value = {
        name: props.engine?.name ?? '',
        description: props.engine?.description ?? '',
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
      ? await api.put('/engine', {
          engine_id: props.engine!.id,
          name: form.value.name,
          description: form.value.description,
          alias: aliasArr
        })
      : await api.post('/engine', {
          name: form.value.name,
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
        {{ isEdit ? '编辑引擎' : '创建引擎' }}
      </h2>

      <KunInput v-model="form.name" label="名称" />
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
