<script setup lang="ts">
const show = defineModel<boolean>({ required: true })
const props = defineProps<{ sites: Site[] }>()
const emit = defineEmits<{ created: [client: OAuthClientCreated] }>()

const api = useApi()

const siteId = ref<number | ''>('')
const name = ref('')
const redirectUris = ref([''])
const error = ref('')
const isLoading = ref(false)

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

  if (!siteId.value || !name.value) {
    error.value = '请选择站点并填写名称'
    return
  }

  const uris = redirectUris.value.filter((u) => u.trim())
  if (uris.length === 0) {
    error.value = '请至少填写一个回调地址'
    return
  }

  isLoading.value = true
  try {
    const response = await api.post<OAuthClientCreated>('/oauth/clients', {
      site_id: Number(siteId.value),
      name: name.value,
      redirect_uris: uris,
      grants: ['authorization_code'],
    })
    if (response.code === 0) {
      emit('created', response.data)
      // Reset form
      siteId.value = ''
      name.value = ''
      redirectUris.value = ['']
    } else {
      error.value = response.message || '创建失败'
    }
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunModal v-model:modal-value="show">
    <div class="w-[28rem] space-y-4 p-6">
      <h2 class="text-xl font-bold text-foreground">创建 OAuth 客户端</h2>

      <div>
        <label class="mb-1 block text-sm font-medium text-default-500">关联站点</label>
        <select
          v-model="siteId"
          class="w-full rounded-lg border border-default-200 bg-content1 px-3 py-2 text-sm text-foreground outline-none focus:border-primary"
        >
          <option value="" disabled>请选择站点</option>
          <option v-for="site in props.sites" :key="site.id" :value="site.id">
            {{ site.name }} ({{ site.domain }})
          </option>
        </select>
      </div>

      <KunInput
        v-model="name"
        label="客户端名称"
        placeholder="例如：KUN Galgame Web"
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
          创建
        </KunButton>
      </div>
    </div>
  </KunModal>
</template>
