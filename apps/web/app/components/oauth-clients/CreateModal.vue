<script setup lang="ts">
import { ALL_GRANTS, KNOWN_SCOPES, DEFAULT_REFRESH_TOKEN_TTL_SECONDS } from '~~/shared/types/oauth-client'

const show = defineModel<boolean>({ required: true })
const props = defineProps<{ sites: Site[] }>()
const emit = defineEmits<{ created: [client: OAuthClientCreated] }>()

const api = useApi()

const siteId = ref<number | ''>('')
const name = ref('')
const redirectUris = ref([''])
// Default both grants — see api/internal/platform/site/handler/site_handler.go.
// authorization_code-only clients break after 15min because refresh hits
// the OAuth grant-allowlist check.
const grants = ref<string[]>(['authorization_code', 'refresh_token'])
// Allowed scopes for this client. Empty array sent to the server falls
// back to the OIDC core set {openid, profile, email} only — anything
// beyond that (e.g. image:upload) MUST be explicitly checked here.
const allowedScopes = ref<string[]>(['openid', 'profile', 'email'])
// Public client (SPA / native): no client_secret on refresh, PKCE required
// on the code flow. Confidential clients are the default (false).
const isPublic = ref(false)
// Refresh token lifetime — exposed in the UI as days for usability,
// converted to seconds at submit time. Default 90d matches the server.
const refreshTokenTtlDays = ref(DEFAULT_REFRESH_TOKEN_TTL_SECONDS / 86400)
const error = ref('')
const isLoading = ref(false)

// KunSelect option list for the site picker (value=id, label=name+domain).
const siteOptions = computed(() =>
  props.sites.map((s) => ({ value: s.id, label: `${s.name} (${s.domain})` }))
)

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

  if (!siteId.value || !name.value) {
    error.value = '请选择站点并填写名称'
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
    const response = await api.post<OAuthClientCreated>('/oauth/clients', {
      site_id: Number(siteId.value),
      name: name.value,
      redirect_uris: uris,
      grants: grants.value,
      allowed_scopes: allowedScopes.value,
      is_public: isPublic.value,
      refresh_token_ttl_seconds: refreshTokenTtlDays.value * 86400,
    })
    if (response.code === 0) {
      emit('created', response.data)
      // Reset form
      siteId.value = ''
      name.value = ''
      redirectUris.value = ['']
      grants.value = ['authorization_code', 'refresh_token']
      allowedScopes.value = ['openid', 'profile', 'email']
      isPublic.value = false
      refreshTokenTtlDays.value = DEFAULT_REFRESH_TOKEN_TTL_SECONDS / 86400
    } else {
      error.value = response.message || '创建失败'
    }
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunModal v-model="show">
    <div class="w-[32rem] space-y-4 p-6">
      <h2 class="text-xl font-bold text-foreground">创建 OAuth 客户端</h2>

      <KunSelect
        v-model="siteId"
        label="关联站点"
        placeholder="请选择站点"
        :options="siteOptions"
      />

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

      <div>
        <label class="mb-1 block text-sm font-medium text-default-500">
          授权类型 (grants)
          <span class="text-xs text-default-400">— refresh_token 必须勾选，否则 15 分钟后用户会被强制重新登录</span>
        </label>
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
        <label class="mb-1 block text-sm font-medium text-default-500">
          允许的 scope (allowed_scopes)
          <span class="text-xs text-default-400">— image:upload 这类敏感 scope 必须显式勾选</span>
        </label>
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
        <label class="mb-1 block text-sm font-medium text-default-500">
          refresh_token 有效期（天）
          <span class="text-xs text-default-400">— 默认 90 天；用户登录后无感续期的最长窗口</span>
        </label>
        <KunInput
          :model-value="refreshTokenTtlDays"
          type="number"
          min="1"
          max="3650"
          @update:model-value="refreshTokenTtlDays = Number($event)"
        />
        <p class="mt-1 text-xs text-default-400">
          常见取值：1（高敏感后台）/ 7 / 30 / <strong>90（默认）</strong> / 365（长寿后台服务）
        </p>
      </div>

      <div class="flex items-center gap-2 text-sm">
        <KunCheckBox
          v-model="isPublic"
          label="公共客户端 (SPA / native)"
          color="primary"
        />
        <span class="text-xs text-default-400">— PKCE 必须；refresh 不需要 client_secret</span>
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
