<script setup lang="ts">
import { needsStepUp } from '~/constants/roles'

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
const accountSwitch = useAccountSwitch()

const isLoading = ref(false)
const error = ref('')
// Multi-account chooser state. `showChooser` gates the AccountChooser render
// (prompt=select_account, or login_hint miss). `bagSessions` is the browser's
// session bag. `switchingSub` drives the per-row spinner during a switch.
const showChooser = ref(false)
const bagSessions = ref<BagSession[]>([])
const switchingSub = ref<string | null>(null)
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
// prompt=select_account forces the multi-account chooser even when an OP
// session exists. login_hint pre-selects a specific account from the bag
// (match by sub or email). See docs/integration/oauth/09.
const promptSelectAccount = computed(() => route.query.prompt === 'select_account')
// prompt=none (OIDC silent flow): never render interactive UI — resolve to the
// RP with the appropriate error via the server-validated error redirect.
const promptNone = computed(() => route.query.prompt === 'none')
const loginHint = computed(() => route.query.login_hint as string | undefined)
// nonce (OIDC) must survive every round-trip on this page (login bounce,
// account switch reload) and reach the consent POST — the backend binds it to
// the auth code and echoes it into the id_token, which standard RP libraries
// verify against the nonce they sent. Dropping it fails their login.
const nonce = computed(() => route.query.nonce as string | undefined)

// Build the full authorize URL. `extra` lets callers re-add params that the
// base URL deliberately omits — notably `login_hint` for the step-up
// round-trip (re-auth lands back here, the hint auto-selects the account).
// `prompt` is ALWAYS omitted so post-login re-entry proceeds to normal
// auto-consent instead of bouncing back into the login/chooser screen.
const buildAuthorizeUrl = (extra?: Record<string, string>) => {
  const params = new URLSearchParams()
  params.set('client_id', clientId.value)
  params.set('redirect_uri', redirectUri.value)
  params.set('response_type', responseType.value)
  if (scope.value) params.set('scope', scope.value)
  params.set('state', state.value)
  if (codeChallenge.value) params.set('code_challenge', codeChallenge.value)
  if (codeChallengeMethod.value) params.set('code_challenge_method', codeChallengeMethod.value)
  if (nonce.value) params.set('nonce', nonce.value)
  if (extra) {
    for (const [k, v] of Object.entries(extra)) params.set(k, v)
  }
  return `/oauth/authorize?${params.toString()}`
}

const currentUrl = computed(() => buildAuthorizeUrl())

const scopeList = computed(() => {
  if (!scope.value) return []
  return scope.value.split(/[\s+]/).filter(Boolean)
})

const scopeLabels: Record<string, string> = {
  openid: '身份标识',
  profile: '用户资料 (昵称、头像)',
  email: '邮箱地址',
}

// Build a server-VALIDATED error redirect and go there (deny + prompt=none).
// The backend checks redirect_uri against the client's registered URIs, so a
// crafted redirect_uri on this public page can't open-redirect; on a validation
// failure we surface the error in-page instead of redirecting anywhere.
const respondWithError = async (errCode: string): Promise<void> => {
  const res = await api.post<{ redirect_url: string }>(
    '/oauth/authorize/error',
    {
      client_id: clientId.value,
      redirect_uri: redirectUri.value,
      state: state.value,
      error: errCode,
    }
  )
  if (res.code === 0) {
    window.location.href = res.data.redirect_url
    return
  }
  autoConsenting.value = false
  needsLogin.value = false
  error.value = res.message || '回调地址未通过校验，未跳转'
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

  // prompt=none (OIDC silent): never render interactive UI — resolve to the RP
  // with the right error via the validated error redirect. Only a live session
  // on an auto_consent (first-party) client proceeds silently to the code.
  if (promptNone.value) {
    if (needsLogin.value || !auth.isLoggedIn.value) {
      await respondWithError('login_required')
      return
    }
    if (!clientInfo.value?.auto_consent) {
      await respondWithError('consent_required')
      return
    }
    await maybeAutoConsent()
    return
  }

  // Multi-account handling (prompt=select_account / login_hint), gated on a
  // live session. Runs BEFORE auto-consent: a hit either silently switches +
  // proceeds, or step-up-redirects; a select_account / hint-miss renders the
  // chooser instead of auto-consenting. When this consumes the request
  // (chooser shown or step-up redirect issued) we return early.
  if (!needsLogin.value && auth.isLoggedIn.value) {
    const handled = await handleMultiAccount()
    if (handled) return
  }

  await maybeAutoConsent()
})

// Resolve prompt=select_account + login_hint against the session bag.
// Returns true when the request is "consumed" (chooser shown or a step-up
// redirect was issued) so onMounted skips auto-consent; false to fall through
// to the normal consent / auto-consent path.
const handleMultiAccount = async (): Promise<boolean> => {
  if (!loginHint.value && !promptSelectAccount.value) return false

  bagSessions.value = await accountSwitch.listBagSessions()

  // login_hint: pre-select a specific account (match by sub or email).
  if (loginHint.value) {
    const hint = loginHint.value
    const match = bagSessions.value.find(
      (s) => s.sub === hint || s.email === hint
    )
    if (match) {
      // Already the active account → nothing to switch; fall through to the
      // normal consent / auto-consent path as that account.
      if (match.active) return false
      const result = await accountSwitch.switchAccount(match.sub)
      if (result.ok) {
        // Reload to the (prompt-stripped) authorize URL so a FRESH useApi reads
        // the new account's token before minting the code. Continuing in-place
        // would consent with the OLD token — Nuxt useCookie refs don't cross-sync
        // between useAccountSwitch and this page's useApi. See useAccountSwitch.
        window.location.href = buildAuthorizeUrl()
        return true
      }
      if (result.stepUp) {
        redirectStepUp(match.sub)
        return true
      }
      // Hint matched but the switch failed (not step-up) → do NOT silently
      // consent as the current account; show the chooser so the user picks.
      showChooser.value = true
      return true
    }
    // Hint not in the bag: show the chooser if select_account was also asked,
    // otherwise fall through to the normal flow as the current account.
    if (!promptSelectAccount.value) return false
  }

  // prompt=select_account (or login_hint miss with select_account): render
  // the chooser, do NOT auto-consent.
  showChooser.value = true
  return true
}

// Auto-consent only fires when actually logged in. The metadata flag
// alone isn't enough — POST /oauth/authorize/consent requires a
// Bearer token, and silently 401-ing in the background would leave
// the user staring at a spinner.
const maybeAutoConsent = async () => {
  if (
    !needsLogin.value &&
    auth.isLoggedIn.value &&
    clientInfo.value?.auto_consent
  ) {
    autoConsenting.value = true
    await handleApprove()
  }
}

// Step-up: switching to a privileged account requires fresh re-auth. Bounce
// to /auth/login with this authorize URL as ?redirect=, KEEPING login_hint=
// <sub> (so the hint auto-selects the freshly-authed account on return) but
// dropping prompt=select_account (buildAuthorizeUrl already omits prompt) so
// re-entry proceeds straight to consent.
const redirectStepUp = (sub: string) => {
  const target = buildAuthorizeUrl({ login_hint: sub })
  // force=1 so the login form actually shows for the re-auth (we're still logged
  // in as the non-privileged account); without it this bounces back to authorize
  // and loops on the step-up. See LoginForm.
  router.push(`/auth/login?force=1&redirect=${encodeURIComponent(target)}`)
}

// Chooser row click: switch to the picked account, then proceed. On ok →
// hide the chooser + run consent/auto-consent (or fall to the manual consent
// UI). On step-up → step-up redirect. On other failure → surface an error and
// keep the chooser open.
const handleChooserPick = async (sub: string) => {
  if (switchingSub.value) return
  // Admin/ren accounts always require fresh re-auth — go straight to the step-up
  // login instead of calling switch (which would just return 10016). We know the
  // role from the bag, so no round-trip / no confusing 401.
  const target = bagSessions.value.find((s) => s.sub === sub)
  if (target && needsStepUp(target.roles)) {
    redirectStepUp(sub)
    return
  }
  switchingSub.value = sub
  error.value = ''
  try {
    const result = await accountSwitch.switchAccount(sub)
    if (result.ok) {
      // Full reload (prompt stripped) so a fresh useApi mints the code with the
      // new account's token — see handleMultiAccount / useAccountSwitch.
      window.location.href = buildAuthorizeUrl()
      return
    }
    if (result.stepUp) {
      redirectStepUp(sub)
      return
    }
    error.value = '切换账号失败，请重试'
  } finally {
    switchingSub.value = null
  }
}

// "使用其他账号登录" → /auth/login with the authorize URL as ?redirect=
// (prompt stripped via buildAuthorizeUrl). After login the user re-enters
// this page as the new account and proceeds to consent.
const handleChooserAdd = () => {
  // force=1: we're logged in (the bag exists), but "add account" must show the
  // login form for a different account rather than bounce back. See LoginForm.
  router.push(`/auth/login?force=1&redirect=${encodeURIComponent(currentUrl.value)}`)
}

// User-initiated login — keeps the OAuth params in the redirect so
// /auth/login chains back to this page, and from there into auto-
// consent + bounce back to redirect_uri. Same destination the pre-fix
// auto-navigateTo used, but now gated on an actual click so the back
// button can escape.
const goLogin = () => {
  // force=1 so /auth/login shows the form even if an OP session still exists.
  // reauth=1 marks this as a forced re-auth (prompt=login) so re-entering the
  // current account COMPLETES the flow instead of stalling on the same-account
  // notice. See LoginForm + docs 09 §3.6.
  router.push(
    `/auth/login?force=1&reauth=1&redirect=${encodeURIComponent(currentUrl.value)}`
  )
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
    if (nonce.value) body.nonce = nonce.value

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

const handleDeny = async () => {
  // Refuse via the server-validated error redirect — no client-side redirect_uri
  // trust (see respondWithError). isLoading covers the round-trip.
  isLoading.value = true
  try {
    await respondWithError('access_denied')
  } finally {
    isLoading.value = false
  }
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

    <!-- Multi-account chooser: prompt=select_account (or a login_hint miss).
         Picking a row switches to that account then proceeds to consent /
         auto-consent; a step-up account bounces to re-auth. -->
    <div v-else-if="showChooser" class="space-y-4">
      <OauthAuthorizeAccountChooser
        :sessions="bagSessions"
        :client-name="clientInfo?.name"
        :switching-sub="switchingSub"
        @pick="handleChooserPick"
        @add="handleChooserAdd"
      />
      <div v-if="error" class="bg-danger-50 text-danger rounded-lg p-3 text-sm">
        {{ error }}
      </div>
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
