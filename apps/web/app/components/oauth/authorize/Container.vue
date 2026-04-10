<script setup lang="ts">
const route = useRoute()
const auth = useAuth()
const api = useApi()

const isLoading = ref(false)
const error = ref('')

// Parse OAuth params from query
const clientId = computed(() => route.query.client_id as string)
const redirectUri = computed(() => route.query.redirect_uri as string)
const responseType = computed(() => route.query.response_type as string)
const scope = computed(() => route.query.scope as string)
const state = computed(() => route.query.state as string)
const codeChallenge = computed(() => route.query.code_challenge as string | undefined)
const codeChallengeMethod = computed(() => route.query.code_challenge_method as string | undefined)

// Build the full authorize URL for login redirect
const currentUrl = computed(() => {
  const params = new URLSearchParams()
  params.set('client_id', clientId.value)
  params.set('redirect_uri', redirectUri.value)
  params.set('response_type', responseType.value)
  if (scope.value) params.set('scope', scope.value)
  params.set('state', state.value)
  if (codeChallenge.value) params.set('code_challenge', codeChallenge.value)
  if (codeChallengeMethod.value) params.set('code_challenge_method', codeChallengeMethod.value)
  return `/oauth/authorize?${params.toString()}`
})

const scopeList = computed(() => {
  if (!scope.value) return []
  return scope.value.split(/[\s+]/).filter(Boolean)
})

const scopeLabels: Record<string, string> = {
  openid: '身份标识',
  profile: '用户资料 (昵称、头像)',
  email: '邮箱地址',
}

// Check login state on mount
onMounted(async () => {
  if (!clientId.value || !redirectUri.value || !state.value) {
    error.value = '缺少必要的 OAuth 参数'
    return
  }

  if (!auth.isLoggedIn.value) {
    // Try refresh first
    const refreshed = await auth.refreshAccessToken()
    if (!refreshed) {
      // Not logged in — redirect to login with return URL
      navigateTo(`/auth/login?redirect=${encodeURIComponent(currentUrl.value)}`)
    }
  }
})

const handleApprove = async () => {
  isLoading.value = true
  error.value = ''

  try {
    const body: Record<string, unknown> = {
      client_id: clientId.value,
      redirect_uri: redirectUri.value,
      response_type: responseType.value,
      scope: scope.value,
      state: state.value,
    }
    if (codeChallenge.value) body.code_challenge = codeChallenge.value
    if (codeChallengeMethod.value) body.code_challenge_method = codeChallengeMethod.value

    const response = await api.post<{ redirect_url: string }>('/oauth/authorize/consent', body)

    if (response.code === 0) {
      // Redirect back to the client app with the authorization code
      window.location.href = response.data.redirect_url
    } else {
      error.value = response.message || '授权失败'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '授权失败'
  } finally {
    isLoading.value = false
  }
}

const handleDeny = () => {
  // Redirect back to client with error
  const url = new URL(redirectUri.value)
  url.searchParams.set('error', 'access_denied')
  url.searchParams.set('error_description', 'User denied the request')
  if (state.value) url.searchParams.set('state', state.value)
  window.location.href = url.toString()
}
</script>

<template>
  <KunCard class="p-8">
    <div v-if="error && !clientId" class="text-center">
      <Icon name="lucide:alert-circle" class="mx-auto mb-4 size-12 text-danger" />
      <p class="text-danger">{{ error }}</p>
    </div>

    <template v-else>
      <div class="mb-6 text-center">
        <Icon name="lucide:shield-check" class="mx-auto mb-3 size-12 text-primary" />
        <h1 class="text-xl font-bold text-foreground">授权请求</h1>
        <p class="mt-2 text-sm text-default-500">
          应用正在请求访问你的账户
        </p>
      </div>

      <div class="mb-6 space-y-3">
        <p class="text-sm font-medium text-foreground">该应用将获得以下权限：</p>
        <ul class="space-y-2">
          <li
            v-for="s in scopeList"
            :key="s"
            class="flex items-center gap-2 text-sm text-default-500"
          >
            <Icon name="lucide:check" class="size-4 text-success" />
            {{ scopeLabels[s] || s }}
          </li>
          <li
            v-if="scopeList.length === 0"
            class="flex items-center gap-2 text-sm text-default-500"
          >
            <Icon name="lucide:check" class="size-4 text-success" />
            基本账户信息
          </li>
        </ul>
      </div>

      <div v-if="error" class="mb-4 rounded-lg bg-danger-50 p-3 text-sm text-danger">
        {{ error }}
      </div>

      <div class="flex gap-3">
        <KunButton color="default" class="flex-1" @click="handleDeny">
          拒绝
        </KunButton>
        <KunButton
          color="primary"
          class="flex-1"
          :disabled="isLoading"
          @click="handleApprove"
        >
          <Icon v-if="isLoading" name="lucide:loader-2" class="mr-2 size-4 animate-spin" />
          {{ isLoading ? '授权中...' : '同意授权' }}
        </KunButton>
      </div>

      <p class="mt-4 text-center text-xs text-default-400">
        授权后将跳转回 {{ redirectUri }}
      </p>
    </template>
  </KunCard>
</template>
