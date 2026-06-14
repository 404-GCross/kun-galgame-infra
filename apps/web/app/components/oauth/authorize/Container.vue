<script setup lang="ts">
interface ClientPublicInfo {
  id: string
  name: string
  auto_consent: boolean
  site_domain: string
}

const route = useRoute()
const router = useRouter()
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
// `needsLogin` is the "no live session" state — set when refresh-token
// recovery fails on mount. We DELIBERATELY don't auto-navigateTo to
// /auth/login here: that pattern (pre-2026-05-24) created a back-button
// trap, since browser-back from /auth/login lands the user on this page
// which immediately re-bounces to /auth/login. The user has no way to
// abort the OAuth flow without closing the tab. Render an explicit
// "登录后继续 / 取消" card instead so cancel + manual login both work.
const needsLogin = ref(false)

// Parse OAuth params from query
const clientId = computed(() => route.query.client_id as string)
const redirectUri = computed(() => route.query.redirect_uri as string)
const responseType = computed(() => route.query.response_type as string)
const scope = computed(() => route.query.scope as string)
const state = computed(() => route.query.state as string)
const codeChallenge = computed(() => route.query.code_challenge as string | undefined)
const codeChallengeMethod = computed(() => route.query.code_challenge_method as string | undefined)
// prompt=login forces the login screen even if an OP session exists — the
// RP-side "log out of this site, re-prompt on next login" path. See
// docs/integration/oauth/07-logout.md. currentUrl deliberately omits `prompt`,
// so after the user logs in, re-entry to this page proceeds to normal
// auto-consent (no loop).
const forceLogin = computed(() => route.query.prompt === 'login')

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

  if (forceLogin.value) {
    // prompt=login: always re-prompt; never silently reuse the OP session.
    needsLogin.value = true
  } else if (!auth.isLoggedIn.value) {
    const refreshed = await auth.refreshAccessToken()
    if (!refreshed) {
      // Show login prompt, NOT auto-navigateTo — see needsLogin comment.
      // We still fall through to fetch metadata so the prompt card can
      // name the requesting client ("「moyu」请求访问你的账户" instead
      // of the anonymous "应用").
      needsLogin.value = true
    }
  }

  // Fetch client metadata. Failure here is non-fatal — fall back to the
  // consent UI (or login prompt) with `clientInfo = null` so the user
  // can still authorize / refuse / log-in even when the metadata
  // endpoint is unhappy. Unauthenticated calls are allowed (the route
  // is public-by-design), so this runs in both login-prompt and
  // post-login paths.
  try {
    const meta = await api.get<ClientPublicInfo>('/oauth/client-info', {
      client_id: clientId.value,
    })
    if (meta.code === 0) {
      clientInfo.value = meta.data
    } else {
      clientInfo.value = null
    }
  } catch {
    clientInfo.value = null
  }

  // Auto-consent only fires when actually logged in. The metadata flag
  // alone isn't enough — POST /oauth/authorize/consent requires a
  // Bearer token, and silently 401-ing in the background would leave
  // the user staring at a spinner.
  if (
    !needsLogin.value &&
    auth.isLoggedIn.value &&
    clientInfo.value?.auto_consent
  ) {
    autoConsenting.value = true
    await handleApprove()
  }
})

// User-initiated login — keeps the OAuth params in the redirect so
// /auth/login chains back to this page, and from there into auto-
// consent + bounce back to redirect_uri. Same destination the pre-fix
// auto-navigateTo used, but now gated on an actual click so the back
// button can escape.
const goLogin = () => {
  router.push(`/auth/login?redirect=${encodeURIComponent(currentUrl.value)}`)
}

const goRegister = () => {
  router.push(`/auth/register?redirect=${encodeURIComponent(currentUrl.value)}`)
}

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
      <KunIcon name="lucide:circle-alert" class="mx-auto mb-4 size-12 text-danger" />
      <p class="text-danger">{{ error }}</p>
    </div>

    <!-- Login-required prompt: unauthenticated user landed here via an
         OAuth authorize redirect. Pre-fix this state auto-navigated to
         /auth/login, creating a back-button loop (user couldn't escape
         back to the originating client). Now we render explicit
         "登录后继续 / 取消" actions so:
           - browser-back into /oauth/authorize lands here (no auto-bounce)
           - "取消" mirrors handleDeny → access_denied error to redirect_uri
           - "登录后继续" goes to /auth/login?redirect=<this URL>, and
             post-login the user re-enters this page logged-in, auto-
             consent fires, they bounce to redirect_uri seamlessly. -->
    <div v-else-if="needsLogin" class="space-y-6">
      <div class="text-center">
        <KunIcon name="lucide:shield-check" class="text-primary mx-auto mb-3 size-12" />
        <h1 class="text-foreground text-xl font-bold">需要登录后授权</h1>
        <p class="text-default-500 mt-2 text-sm">
          <template v-if="clientInfo">
            「<span class="text-foreground font-medium">{{ clientInfo.name }}</span>」请求访问你的账户
          </template>
          <template v-else>
            一个应用请求访问你的账户
          </template>
        </p>
        <p class="text-default-400 mt-1 text-xs">
          登录后将自动完成授权，无需额外操作
        </p>
      </div>

      <div class="flex gap-3">
        <KunButton color="default" class="flex-1" @click="handleDeny">
          取消
        </KunButton>
        <KunButton color="primary" class="flex-1" @click="goLogin">
          登录后继续
        </KunButton>
      </div>

      <p class="text-default-500 text-center text-sm">
        还没有账号？
        <button
          type="button"
          class="text-primary hover:underline"
          @click="goRegister"
        >立即注册</button>
      </p>
    </div>

    <!-- Auto-consent path: first-party client (kungal / moyu / wiki / ...).
         No consent question rendered — show a brief "redirecting" spinner
         while we POST /oauth/authorize/consent + bounce to redirect_uri.
         Typical wall-clock time visible to user: ~150 ms. -->
    <div v-else-if="autoConsenting || clientInfo === undefined" class="py-8 text-center">
      <KunIcon name="lucide:loader-circle" class="text-primary mx-auto mb-3 size-8 animate-spin" />
      <p class="text-default-500 text-sm">
        {{ autoConsenting ? '正在跳转回应用...' : '加载中...' }}
      </p>
    </div>

    <template v-else>
      <div class="mb-6 text-center">
        <KunIcon name="lucide:shield-check" class="mx-auto mb-3 size-12 text-primary" />
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
            <KunIcon name="lucide:check" class="size-4 text-success" />
            {{ scopeLabels[s] || s }}
          </li>
          <li
            v-if="scopeList.length === 0"
            class="flex items-center gap-2 text-sm text-default-500"
          >
            <KunIcon name="lucide:check" class="size-4 text-success" />
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
          <KunIcon v-if="isLoading" name="lucide:loader-circle" class="mr-2 size-4 animate-spin" />
          {{ isLoading ? '授权中...' : '同意授权' }}
        </KunButton>
      </div>

      <p class="mt-4 text-center text-xs text-default-400">
        授权后将跳转回 {{ redirectUri }}
      </p>
    </template>
  </KunCard>
</template>
