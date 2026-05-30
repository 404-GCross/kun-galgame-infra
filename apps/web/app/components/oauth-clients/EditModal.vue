<script setup lang="ts">
import { ALL_GRANTS, KNOWN_SCOPES, DEFAULT_REFRESH_TOKEN_TTL_SECONDS } from '~~/shared/types/oauth-client'

const props = defineProps<{ client: OAuthClient }>()
const emit = defineEmits<{ close: []; updated: [] }>()

const api = useApi()
const show = ref(true)

const name = ref(props.client.name)
const redirectUris = ref([...props.client.redirect_uris])
const grants = ref<string[]>([...(props.client.grants ?? [])])
const allowedScopes = ref<string[]>([...(props.client.allowed_scopes ?? [])])
const refreshTokenTtlDays = ref(
  Math.round(
    (props.client.refresh_token_ttl_seconds ?? DEFAULT_REFRESH_TOKEN_TTL_SECONDS) / 86400
  )
)
// is_public is set at create time; toggling it post-hoc is dangerous
// (changes the auth model). We surface it read-only here — if someone
// really needs to flip it, they should recreate the client.
const isPublicReadonly = computed(() => props.client.is_public ?? false)
// auto_consent IS editable post-hoc — it only affects consent-screen
// rendering, no token semantics change. Toggling on/off takes effect
// on the next /oauth/authorize visit for this client.
const autoConsent = ref(props.client.auto_consent ?? false)
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

const toggleGrant = (g: string) => {
  const i = grants.value.indexOf(g)
  if (i >= 0) {
    grants.value.splice(i, 1)
  } else {
    grants.value.push(g)
  }
}

const toggleScope = (s: string) => {
  const i = allowedScopes.value.indexOf(s)
  if (i >= 0) {
    allowedScopes.value.splice(i, 1)
  } else {
    allowedScopes.value.push(s)
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

  if (grants.value.length === 0) {
    error.value = '请至少选择一种 grant 类型'
    return
  }

  if (refreshTokenTtlDays.value < 1) {
    error.value = 'refresh_token 有效期至少 1 天'
    return
  }

  isLoading.value = true
  try {
    const response = await api.put(`/oauth/clients/${props.client.id}`, {
      name: name.value,
      redirect_uris: uris,
      grants: grants.value,
      allowed_scopes: allowedScopes.value,
      auto_consent: autoConsent.value,
      refresh_token_ttl_seconds: refreshTokenTtlDays.value * 86400,
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
  <KunModal v-model="show">
    <div class="w-[32rem] max-w-[calc(100vw-1.5rem)] space-y-4 p-6">
      <h2 class="text-xl font-bold text-foreground">编辑客户端</h2>

      <div class="rounded-lg bg-default-50 p-3">
        <p class="text-xs text-default-400">Client ID</p>
        <p class="mt-1 truncate font-mono text-sm text-foreground">{{ client.id }}</p>
        <p class="mt-2 text-xs text-default-400">
          类型：{{ isPublicReadonly ? '公共客户端 (SPA / native)' : '机密客户端 (confidential)' }}
          <span class="text-default-300">— 创建后不可修改</span>
        </p>
      </div>

      <KunInput
        v-model="name"
        label="客户端名称"
        placeholder="客户端名称"
        required
      />

      <div>
        <span class="mb-1 block text-sm font-medium text-default-500">回调地址</span>
        <div class="space-y-2">
          <div v-for="(_, index) in redirectUris" :key="index" class="flex gap-2">
            <KunInput
              v-model="redirectUris[index]"
              placeholder="https://example.com/auth/callback"
              class="flex-1"
            />
            <KunButton
              v-if="redirectUris.length > 1"
              variant="light"
              color="danger"
              size="sm"
              is-icon-only
              aria-label="移除回调地址"
              class-name="shrink-0"
              @click="removeUri(index)"
            >
              <Icon name="lucide:x" class="size-4" />
            </KunButton>
          </div>
        </div>
        <KunButton
          variant="light"
          color="primary"
          size="sm"
          class-name="mt-2"
          @click="addUri"
        >
          <Icon name="lucide:plus" class="mr-1 size-3" />
          添加回调地址
        </KunButton>
      </div>

      <div>
        <span class="mb-1 block text-sm font-medium text-default-500">
          授权类型 (grants)
          <span class="text-xs text-default-400">— refresh_token 必须勾选，否则 15 分钟后用户会被强制重新登录</span>
        </span>
        <div class="flex flex-wrap gap-2">
          <KunCheckBox
            v-for="g in ALL_GRANTS"
            :key="g"
            :model-value="grants.includes(g)"
            :label="g"
            color="primary"
            class-name="rounded-lg border border-default-200 bg-content1 px-3 py-1.5 hover:border-primary"
            @update:model-value="toggleGrant(g)"
          />
        </div>
      </div>

      <div>
        <span class="mb-1 block text-sm font-medium text-default-500">
          允许的 scope (allowed_scopes)
          <span class="text-xs text-default-400">— image:upload 这类敏感 scope 必须显式勾选</span>
        </span>
        <div class="flex flex-wrap gap-2">
          <KunCheckBox
            v-for="s in KNOWN_SCOPES"
            :key="s"
            :model-value="allowedScopes.includes(s)"
            :label="s"
            color="primary"
            class-name="rounded-lg border border-default-200 bg-content1 px-3 py-1.5 hover:border-primary"
            @update:model-value="toggleScope(s)"
          />
        </div>
      </div>

      <div>
        <span class="mb-1 block text-sm font-medium text-default-500">
          refresh_token 有效期（天）
          <span class="text-xs text-default-400">— 改动仅影响后续新签发的 token；现有 session 仍按旧 TTL</span>
        </span>
        <KunInput
          :model-value="refreshTokenTtlDays"
          type="number"
          min="1"
          max="3650"
          @update:model-value="refreshTokenTtlDays = Number($event)"
        />
      </div>

      <div class="rounded-lg border border-warning-200 bg-warning-50 p-3">
        <div class="flex items-center gap-2 text-sm">
          <KunCheckBox
            v-model="autoConsent"
            label="自动同意 (第一方应用专用)"
            color="warning"
          />
        </div>
        <p class="mt-2 text-xs text-warning-700">
          ⚠️ 勾选后此应用的用户在 OAuth 授权页将
          <strong>跳过手动"同意"步骤</strong>，直接静默授权。
          <strong class="text-danger">仅用于你完全信任的第一方应用</strong>。
          切换会在下次 /oauth/authorize 访问立即生效，不影响已签发的 token。
        </p>
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
