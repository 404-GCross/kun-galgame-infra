<script setup lang="ts">
interface ClientPublicInfo {
  id: string
  name: string
  auto_consent: boolean
  site_domain: string
}

const route = useRoute()
const auth = useAuth()
const api = useApi()

const isLoading = ref(false)
const error = ref('')
// `clientInfo === null` after fetch = lookup failed (probably bad client_id);
// `=== undefined` = still loading. `auto_consent` drives the silent grant
// path so we render skeleton state until we know which branch to take —
// flashing a consent card and then immediately auto-dismissing it is
// worse UX than waiting one extra request worth of time.
const clientInfo = ref<ClientPublicInfo | null | undefined>(undefined)
const autoConsenting = ref(false)

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

// Check login state on mount + decide auto-consent.
//
// Order matters: we must ensure the user is logged in BEFORE we attempt
// auto-consent — POST /oauth/authorize/consent requires a Bearer token.
// 1. Validate OAuth params shape (fast fail)
// 2. Ensure logged-in (try silent refresh; otherwise bounce to /auth/login
//    with this whole URL as ?redirect=)
// 3. Fetch client metadata; if auto_consent=true → silently approve
//    (zero UI render between landing here and bouncing to redirect_uri)
// 4. Otherwise fall through to the regular consent UI
//
// For unified registration: a freshly-registered user already has
// access_token (Register endpoint issues it). They land here logged-in,
// metadata fetch returns auto_consent=true for first-party kungal/moyu,
// and we redirect straight to the client without showing any extra UI.
onMounted(async () => {
  if (!clientId.value || !redirectUri.value || !state.value) {
    error.value = '缺少必要的 OAuth 参数'
    return
  }

  if (!auth.isLoggedIn.value) {
    const refreshed = await auth.refreshAccessToken()
    if (!refreshed) {
      navigateTo(`/auth/login?redirect=${encodeURIComponent(currentUrl.value)}`)
      return
    }
  }

  // Fetch client metadata. Failure here is non-fatal — fall back to the
  // consent UI with `clientInfo = null` so the user can still authorize
  // (or refuse) even when the metadata endpoint is unhappy.
  try {
    const meta = await api.get<ClientPublicInfo>('/oauth/client-info', {
      client_id: clientId.value,
    })
    if (meta.code === 0) {
      clientInfo.value = meta.data
      if (meta.data.auto_consent) {
        autoConsenting.value = true
        await handleApprove()
        return
      }
    } else {
      clientInfo.value = null
    }
  } catch {
    clientInfo.value = null
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
      // Drop out of auto-consent so the user sees the error + manual
      // approve/deny buttons instead of being stuck on the "正在跳转回
      // 应用..." spinner forever. Same goes for the catch branch below.
      autoConsenting.value = false
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '授权失败'
    autoConsenting.value = false
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

    <!-- Auto-consent path: first-party client (kungal / moyu / wiki / ...).
         No consent question rendered — show a brief "redirecting" spinner
         while we POST /oauth/authorize/consent + bounce to redirect_uri.
         Typical wall-clock time visible to user: ~150 ms. -->
    <div v-else-if="autoConsenting || clientInfo === undefined" class="py-8 text-center">
      <Icon name="lucide:loader-2" class="mx-auto mb-3 size-8 animate-spin text-primary" />
      <p class="text-sm text-default-500">
        {{ autoConsenting ? '正在跳转回应用...' : '加载中...' }}
      </p>
    </div>

    <template v-else>
      <div class="mb-6 text-center">
        <Icon name="lucide:shield-check" class="mx-auto mb-3 size-12 text-primary" />
        <h1 class="text-xl font-bold text-foreground">授权请求</h1>
        <p class="mt-2 text-sm text-default-500">
          <span v-if="clientInfo">「{{ clientInfo.name }}」</span>
          正在请求访问你的账户
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
