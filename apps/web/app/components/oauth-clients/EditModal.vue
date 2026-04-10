<script setup lang="ts">
const props = defineProps<{ client: OAuthClient }>()
const emit = defineEmits<{ close: []; updated: [] }>()

const api = useApi()
const show = ref(true)

const name = ref(props.client.name)
const redirectUris = ref([...props.client.redirect_uris])
const error = ref('')
const isLoading = ref(false)

watch(show, (val) => {
  if (!val) emit('close')
})

const addUri = () => {
  redirectUris.value.push('')
}

const removeUri = (index: number) => {
  if (redirectUris.value.length > 1) {
    redirectUris.value.splice(index, 1)
  }
}

const handleSubmit = async () => {
  error.value = ''

  if (!name.value) {
    error.value = '请填写名称'
    return
  }

  const uris = redirectUris.value.filter((u) => u.trim())
  if (uris.length === 0) {
    error.value = '请至少填写一个回调地址'
    return
  }

  isLoading.value = true
  try {
    const response = await api.put(`/oauth/clients/${props.client.id}`, {
      name: name.value,
      redirect_uris: uris,
    })
    if (response.code === 0) {
      emit('updated')
    } else {
      error.value = response.message || '更新失败'
    }
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunModal v-model:modal-value="show">
    <div class="w-[28rem] space-y-4 p-6">
      <h2 class="text-xl font-bold text-foreground">编辑客户端</h2>

      <div class="rounded-lg bg-default-50 p-3">
        <p class="text-xs text-default-400">Client ID</p>
        <p class="mt-1 truncate font-mono text-sm text-foreground">{{ client.id }}</p>
      </div>

      <KunInput
        v-model="name"
        label="客户端名称"
        placeholder="客户端名称"
        required
      />

      <div>
        <label class="mb-1 block text-sm font-medium text-default-500">回调地址</label>
        <div class="space-y-2">
          <div v-for="(_, index) in redirectUris" :key="index" class="flex gap-2">
            <KunInput
              v-model="redirectUris[index]"
              placeholder="https://example.com/auth/callback"
              class="flex-1"
            />
            <button
              v-if="redirectUris.length > 1"
              class="shrink-0 rounded-lg p-2 text-default-300 hover:bg-danger-50 hover:text-danger"
              @click="removeUri(index)"
            >
              <Icon name="lucide:x" class="size-4" />
            </button>
          </div>
        </div>
        <button
          class="mt-2 flex items-center gap-1 text-sm text-primary hover:underline"
          @click="addUri"
        >
          <Icon name="lucide:plus" class="size-3" />
          添加回调地址
        </button>
      </div>

      <div v-if="error" class="rounded-lg bg-danger-50 p-3 text-sm text-danger">
        {{ error }}
      </div>

      <div class="flex justify-end gap-3">
        <KunButton color="default" variant="flat" @click="show = false">
          取消
        </KunButton>
        <KunButton color="primary" :disabled="isLoading" @click="handleSubmit">
          <Icon v-if="isLoading" name="lucide:loader-2" class="mr-2 size-4 animate-spin" />
          保存
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>
