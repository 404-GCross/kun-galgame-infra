<script setup lang="ts">
import type { DevApp } from '~~/shared/types/dev'

// Create a new application (POST /dev/apps). name required (≤100), description
// optional (≤100). The 5-per-account limit is enforced server-side; its 400
// message is surfaced verbatim.
const emit = defineEmits<{ close: []; created: [DevApp] }>()

const api = useApi()
const show = ref(true)

const name = ref('')
const description = ref('')
const error = ref('')
const isLoading = ref(false)

watch(show, (val) => {
  if (!val) emit('close')
})

const handleSubmit = async () => {
  error.value = ''
  if (!name.value.trim()) {
    error.value = '请填写应用名称'
    return
  }
  isLoading.value = true
  try {
    // Wire body mirrors the 06a POST /dev/apps contract (name + optional
    // description).
    const body: Record<string, unknown> = { name: name.value.trim() }
    if (description.value.trim()) body.description = description.value.trim()
    const res = await api.post<DevApp>('/dev/apps', body)
    if (res.code === 0 && res.data) {
      useKunMessage('应用已创建', 'success')
      emit('created', res.data)
    } else {
      error.value = res.message || '创建失败'
    }
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunModal v-model="show" size="md">
    <div class="space-y-4">
      <h2 class="text-xl font-bold text-foreground">创建应用</h2>

      <KunInput
        v-model="name"
        label="应用名称"
        placeholder="例如：我的 Galgame 管理器"
        required
      />

      <KunInput
        v-model="description"
        label="应用描述（可选）"
        placeholder="一句话描述用途,最多 100 字"
      />

      <div v-if="error" class="rounded-lg bg-danger-50 p-3 text-sm text-danger">
        {{ error }}
      </div>

      <div class="flex justify-end gap-3">
        <KunButton color="default" variant="flat" @click="show = false">
          取消
        </KunButton>
        <KunButton color="primary" :disabled="isLoading" @click="handleSubmit">
          <KunIcon
            v-if="isLoading"
            name="lucide:loader-circle"
            class="mr-2 size-4 animate-spin"
          />
          创建
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>
