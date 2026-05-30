<script setup lang="ts">
// Two-step register UI mirrors EmailChange.vue's verify-with-code pattern.
// Step 1: user fills name + email + password, clicks "发送验证码" → the
// "确认注册" button + code input appear, a 60s cool-down timer ticks.
// Step 2: user pastes the 6-digit code, clicks "确认注册" → backend
// verifies code + creates account + issues tokens (we drop straight into
// logged-in state via useAuth.register's internal setAccessToken /
// userStore.setUser).
//
// `?redirect=` chains the post-register navigation into a downstream
// OAuth authorize URL when this page was reached via the unified
// registration flow (kungal/moyu/wiki "注册" button → here → back to
// origin site already logged in). Mirrors LoginForm's `redirect` query
// param exactly so unified PKCE helpers can target either endpoint.
const auth = useAuth()
const router = useRouter()
const route = useRoute()

const name = ref('')
const email = ref('')
const password = ref('')
const confirmPassword = ref('')
const code = ref('')

const error = ref('')
const success = ref('')
const isLoading = ref(false)
const codeSent = ref(false)
const countdown = ref(0)

let timer: ReturnType<typeof setInterval> | null = null

const startCountdown = () => {
  countdown.value = 60
  timer = setInterval(() => {
    countdown.value--
    if (countdown.value <= 0 && timer) {
      clearInterval(timer)
      timer = null
    }
  }, 1000)
}

onUnmounted(() => {
  if (timer) clearInterval(timer)
})

const redirectUrl = computed(() => route.query.redirect as string | undefined)

const navigateAfterRegister = () => {
  if (redirectUrl.value) {
    if (redirectUrl.value.startsWith('/')) {
      router.push(redirectUrl.value)
    } else {
      window.location.href = redirectUrl.value
    }
  } else {
    router.push('/profile')
  }
}

// Already-logged-in user landing here: skip the form entirely. See
// LoginForm for the same pattern.
onMounted(() => {
  if (auth.isLoggedIn.value) {
    navigateAfterRegister()
  }
})

// Client-side preflight before hitting the backend with send-code.
// Backend validates the same constraints + checks uniqueness, but
// catching trivial issues here saves a round-trip + avoids burning a
// strict-rate-limited slot on obvious errors.
//
// Name rule mirrors backend `utils.IsValidName` (legacy isValidName):
//   - 1..17 chars, Unicode letter/number + `!~_@#$%^&*()+=-`
//   - rejects 50+ invisible / zero-width / format-control codepoints
//     that would let an attacker register a visually-identical name.
// If this rule drifts from backend, the backend wins — frontend
// validation is purely UX, never a security boundary.
const NAME_REGEX = /^[\p{L}\p{N}!~_@#$%^&*()+=-]{1,17}$/u
const INVISIBLE_NAME_CODEPOINTS = [
  0x0009, 0x0020, 0x00a0, 0x00ad, 0x034f, 0x061c,
  0x115f, 0x1160, 0x17b4, 0x17b5, 0x180e,
  0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005,
  0x2006, 0x2007, 0x2008, 0x2009, 0x200a, 0x200b,
  0x200c, 0x200d, 0x200e, 0x200f, 0x202f, 0x205f,
  0x2060, 0x2061, 0x2062, 0x2063, 0x2064, 0x2065,
  0x206a, 0x206b, 0x206c, 0x206d, 0x206e, 0x206f,
  0x3000, 0x2800, 0x3164, 0xfeff, 0xffa0,
  0x1d159, 0x1d173, 0x1d174, 0x1d175, 0x1d176,
  0x1d177, 0x1d178, 0x1d179, 0x1d17a, 0xe0020
]
const isValidName = (name: string): boolean => {
  for (const cp of INVISIBLE_NAME_CODEPOINTS) {
    if (name.includes(String.fromCodePoint(cp))) return false
  }
  return NAME_REGEX.test(name)
}

const validateStepOne = (): boolean => {
  if (!isValidName(name.value)) {
    error.value =
      '用户名需为 1 到 17 个字符，仅允许字母 / 数字 / 中文 / `!~_@#$%^&*()+=-`，且不可含空格、零宽字符等不可见符号'
    return false
  }
  if (!email.value || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.value)) {
    error.value = '请输入合法的邮箱地址'
    return false
  }
  if (password.value.length < 6) {
    error.value = '密码长度至少为 6 位'
    return false
  }
  if (password.value !== confirmPassword.value) {
    error.value = '两次输入的密码不一致'
    return false
  }
  return true
}

const handleSendCode = async () => {
  error.value = ''
  success.value = ''

  if (!validateStepOne()) return

  isLoading.value = true
  try {
    const response = await auth.sendRegisterCode(name.value, email.value)
    if (response.code === 0) {
      codeSent.value = true
      success.value = '验证码已发送到你的邮箱，请查收（如未收到请检查垃圾箱）'
      startCountdown()
    } else {
      error.value = response.message || '发送失败'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '发送失败'
  } finally {
    isLoading.value = false
  }
}

const handleRegister = async () => {
  error.value = ''
  success.value = ''

  if (!code.value || code.value.length !== 6) {
    error.value = '请输入 6 位验证码'
    return
  }

  isLoading.value = true
  try {
    const response = await auth.register(
      name.value,
      email.value,
      password.value,
      code.value
    )
    if (response.code === 0) {
      navigateAfterRegister()
    } else {
      error.value = response.message || '注册失败'
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '注册失败'
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <KunCard class="p-8">
    <div class="mb-8 text-center">
      <h1 class="text-2xl font-bold text-foreground">创建账号</h1>
      <p class="mt-2 text-default-500">加入 KUN Visual Novel 社区</p>
    </div>

    <form @submit.prevent>
      <div class="space-y-4">
        <!-- Step 1 fields. Locked after the code is sent so consumers
             can't tweak the email and submit a stale code against a
             different address. To change them: wait out the countdown,
             click 重新发送 (which only resends; backend hashes the new
             email under a new key). -->
        <KunInput
          v-model="name"
          label="用户名"
          type="text"
          placeholder="2 到 17 字符"
          required
          autofocus
          :disabled="codeSent"
        />
        <KunInput
          v-model="email"
          label="邮箱"
          type="email"
          placeholder="将寄送验证码到此邮箱"
          required
          :disabled="codeSent"
        />
        <KunInput
          v-model="password"
          label="密码"
          type="password"
          placeholder="至少 6 位"
          required
          :disabled="codeSent"
        />
        <KunInput
          v-model="confirmPassword"
          label="确认密码"
          type="password"
          placeholder="请再次输入密码"
          required
          :disabled="codeSent"
        />

        <KunInput
          v-if="codeSent"
          v-model="code"
          label="验证码"
          type="text"
          placeholder="请输入 6 位验证码"
          maxlength="6"
          autofocus
        />

        <div v-if="error" class="rounded-lg bg-danger-50 p-3 text-sm text-danger">{{ error }}</div>
        <div v-if="success" class="rounded-lg bg-success-50 p-3 text-sm text-success">{{ success }}</div>

        <div class="flex gap-3">
          <KunButton
            v-if="!codeSent"
            type="button"
            color="primary"
            class="w-full"
            :disabled="isLoading"
            @click="handleSendCode"
          >
            <Icon v-if="isLoading" name="lucide:loader-2" class="mr-2 size-4 animate-spin" />
            {{ isLoading ? '发送中...' : '发送验证码' }}
          </KunButton>

          <template v-else>
            <KunButton
              type="button"
              color="default"
              :disabled="countdown > 0 || isLoading"
              @click="handleSendCode"
            >
              {{ countdown > 0 ? `${countdown}s` : '重新发送' }}
            </KunButton>
            <KunButton
              type="button"
              color="primary"
              class="flex-1"
              :disabled="isLoading || !code"
              @click="handleRegister"
            >
              <Icon v-if="isLoading" name="lucide:loader-2" class="mr-2 size-4 animate-spin" />
              {{ isLoading ? '注册中...' : '确认注册' }}
            </KunButton>
          </template>
        </div>
      </div>
    </form>

    <div class="mt-6 text-center text-sm">
      <p class="text-default-500">
        已有账号？
        <NuxtLink
          :to="redirectUrl ? `/auth/login?redirect=${encodeURIComponent(redirectUrl)}` : '/auth/login'"
          class="text-primary hover:underline"
        >立即登录</NuxtLink>
      </p>
    </div>
  </KunCard>
</template>
