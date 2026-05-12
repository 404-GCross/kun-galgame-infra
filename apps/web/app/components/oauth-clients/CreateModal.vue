<script setup lang="ts">
import { ALL_GRANTS, KNOWN_SCOPES } from '~/shared/types/oauth-client'

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

  isLoading.value = true
  try {
    const response = await api.post<OAuthClientCreated>('/oauth/clients', {
      site_id: Number(siteId.value),
      name: name.value,
      redirect_uris: uris,
      grants: grants.value,
      allowed_scopes: allowedScopes.value,
      is_public: isPublic.value,
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
    <div class="w-[32rem] space-y-4 p-6">
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

      <div>
        <label class="mb-1 block text-sm font-medium text-default-500">
          授权类型 (grants)
          <span class="text-xs text-default-400">— refresh_token 必须勾选，否则 15 分钟后用户会被强制重新登录</span>
        </label>
        <div class="flex flex-wrap gap-2">
          <label
            v-for="g in ALL_GRANTS"
            :key="g"
            class="flex cursor-pointer items-center gap-1.5 rounded-lg border border-default-200 bg-content1 px-3 py-1.5 text-sm text-foreground hover:border-primary"
          >
            <input
              type="checkbox"
              :checked="grants.includes(g)"
              class="size-3.5 accent-primary"
              @change="toggleGrant(g)"
            />
            {{ g }}
          </label>
        </div>
      </div>

      <div>
        <label class="mb-1 block text-sm font-medium text-default-500">
          允许的 scope (allowed_scopes)
          <span class="text-xs text-default-400">— image:upload 这类敏感 scope 必须显式勾选</span>
        </label>
        <div class="flex flex-wrap gap-2">
          <label
            v-for="s in KNOWN_SCOPES"
            :key="s"
            class="flex cursor-pointer items-center gap-1.5 rounded-lg border border-default-200 bg-content1 px-3 py-1.5 text-sm text-foreground hover:border-primary"
          >
            <input
              type="checkbox"
              :checked="allowedScopes.includes(s)"
              class="size-3.5 accent-primary"
              @change="toggleScope(s)"
            />
            {{ s }}
          </label>
        </div>
      </div>

      <div>
        <label class="flex cursor-pointer items-center gap-2 text-sm font-medium text-default-500">
          <input
            v-model="isPublic"
            type="checkbox"
            class="size-3.5 accent-primary"
          />
          公共客户端 (SPA / native)
          <span class="text-xs text-default-400 font-normal">— PKCE 必须；refresh 不需要 client_secret</span>
        </label>
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
