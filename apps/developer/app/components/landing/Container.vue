<script setup lang="ts">
import { API_BASE_URL } from '~/constants/dev'

const auth = useAuth()
const { open: openLogin } = useLoginModal()

useSeoMeta({
  title: '开发者平台',
  description:
    'NextMoe Codex — ACGN 数据的权威正典。当各源各执一词，以 NextMoe 为准。首发 Galgame 面：VNDB / Bangumi / DLsite / ErogameScape 四源裁定；注册应用、领取密钥，几分钟内发出第一个请求。',
  ogTitle: 'NextMoe Codex 开发者平台',
  ogDescription: 'ACGN 数据，以此为准 — one canon, every source reconciled.'
})

const stats = [
  { value: '21 万+', label: '作品注册' },
  { value: '4 源', label: 'VNDB · Bangumi · DLsite · EG' },
  { value: '63 万+', label: 'credits 关系' },
  { value: '/v1', label: '版本化稳定契约' }
]

const quickstart = [
  {
    icon: 'lucide:log-in',
    title: '登录生态账号',
    body: '使用你的 NextMoe（鲲 Galgame）账号登录，无需另行注册开发者身份。'
  },
  {
    icon: 'lucide:layout-dashboard',
    title: '创建应用',
    body: '在控制台创建一个应用（每个账号最多 5 个），即刻获得独立的配额与用量视图。'
  },
  {
    icon: 'lucide:key-round',
    title: '领取 API Key',
    body: '生成密钥并妥善保存（仅显示一次）。带上它请求任意公开面，一把 key 走遍全生态。'
  }
]

const faces = [
  {
    icon: 'lucide:gamepad-2',
    name: 'Galgame 面',
    path: '/v1/galgame',
    body: '正典记录:多源裁定的名称 / 简介 / 封面 / 标签 / 会社 / 发售 / 三源评分,含变更流增量同步。'
  },
  {
    icon: 'lucide:network',
    name: 'Catalog 面',
    path: '/v1/catalog',
    body: '跨媒介身份正典:作品 / 人物名义 / 角色 / 厂牌 / credits / 关系,外部 id 反查四源锚。'
  }
]

const features = [
  {
    icon: 'lucide:layers',
    title: '正典记录',
    body: '每个字段都是多源裁定后的标准答案,响应携带 attribution 归源 —— 不是任何单一源的转发,而是可引用的结论。'
  },
  {
    icon: 'lucide:shield-check',
    title: '鉴权',
    body: '服务端以 Authorization: Bearer nm_live_… 发送。密钥是机密,仅服务端使用。'
  },
  {
    icon: 'lucide:gauge',
    title: '限流与配额',
    body: '按 tier 分层的每分钟限流与每日配额,响应头携带剩余额度;公开读经 Cloudflare 边缘缓存。'
  },
  {
    icon: 'lucide:git-branch',
    title: '稳定契约',
    body: 'URL 版本化 /v1;已发布字段只做向后兼容的新增,破坏性变更升 /v2 并给足迁移窗口。'
  }
]

const curlSample = `curl https://api.nextmoe.dev/v1/galgame/1 \\
  -H "Authorization: Bearer nm_live_…"`
</script>

<template>
  <div class="space-y-24">
    <!-- Hero -->
    <section
      class="grid items-center gap-10 pt-4 md:pt-10 lg:grid-cols-2 lg:gap-14"
    >
      <!-- Copy -->
      <div class="text-center lg:text-left">
        <div
          class="inline-flex items-center gap-2 rounded-full border border-default-200 bg-content1 px-3 py-1 text-xs font-medium text-default-500"
        >
          <span class="size-2 rounded-full bg-success" />
          Phase 1 · 公开只读 API
        </div>

        <h1
          class="mt-6 text-4xl font-bold tracking-tight text-foreground md:text-5xl md:leading-[1.1] lg:text-6xl"
        >
          ACGN 数据的<br class="hidden sm:inline" />
          权威正典
        </h1>
        <p
          class="mt-4 text-sm font-medium tracking-wide text-primary lg:text-base"
        >
          One canon. Every source reconciled.
        </p>
        <p
          class="mx-auto mt-5 max-w-xl text-lg leading-relaxed text-default-500 lg:mx-0"
        >
          当 VNDB、Bangumi、DLsite、ErogameScape 各执一词,
          NextMoe 给出唯一的标准答案 —— 每个字段皆经多源裁定、可溯源、
          可增量同步。从 Galgame 起步,同构扩展至全部 ACGN 媒介。以此为准。
        </p>

        <div
          class="mt-8 flex flex-wrap items-center justify-center gap-3 lg:justify-start"
        >
          <KunButton
            v-if="auth.isLoggedIn.value"
            color="primary"
            size="lg"
            @click="navigateTo('/dashboard')"
          >
            进入控制台
            <KunIcon name="lucide:arrow-right" class="ml-1 size-4" />
          </KunButton>
          <KunButton v-else color="primary" size="lg" @click="openLogin()">
            登录开始
            <KunIcon name="lucide:arrow-right" class="ml-1 size-4" />
          </KunButton>
          <KunButton variant="flat" size="lg" @click="navigateTo('/docs')">
            <KunIcon name="lucide:book-open" class="mr-1 size-4" />
            查看 API 文档
          </KunButton>
        </div>

        <div
          class="mt-6 inline-flex items-center gap-2 rounded-lg border border-default-200 bg-content1 px-3 py-1.5"
        >
          <span class="text-xs text-default-400">Base URL</span>
          <span class="font-mono text-sm text-foreground">{{
            API_BASE_URL
          }}</span>
          <DocsCopyButton :text="API_BASE_URL" label="复制 Base URL" />
        </div>
      </div>

      <!-- Request / response showcase -->
      <div
        class="overflow-hidden rounded-2xl border border-default-200 bg-content1 shadow-sm"
      >
        <div
          class="flex items-center gap-2 border-b border-default-200 px-4 py-3"
        >
          <span class="size-3 rounded-full bg-danger/50" />
          <span class="size-3 rounded-full bg-warning/50" />
          <span class="size-3 rounded-full bg-success/50" />
          <span class="ml-2 font-mono text-xs text-default-400">
            api.nextmoe.dev
          </span>
          <DocsCopyButton
            :text="curlSample"
            label="复制 curl 示例"
            class="ml-auto"
          />
        </div>
        <div class="space-y-4 px-4 py-4 font-mono text-xs leading-relaxed">
          <pre
            class="overflow-x-auto text-default-600"
          ><code>{{ curlSample }}</code></pre>
          <div class="space-y-1 border-t border-default-200 pt-4">
            <p class="font-semibold text-success">200 OK</p>
            <p class="text-default-400">
              cache-control:
              <span class="text-default-600">public, s-maxage=86400</span>
            </p>
            <p class="text-default-400">
              x-ratelimit-remaining:
              <span class="text-default-600">59</span>
            </p>
            <p class="text-default-400">
              x-quota-remaining:
              <span class="text-default-600">49999</span>
            </p>
          </div>
        </div>
      </div>
    </section>

    <!-- Stats band -->
    <section
      class="grid grid-cols-2 gap-px overflow-hidden rounded-2xl border border-default-200 bg-default-200 md:grid-cols-4"
    >
      <div
        v-for="stat in stats"
        :key="stat.label"
        class="bg-background px-6 py-7 text-center"
      >
        <p class="text-2xl font-bold text-foreground md:text-3xl">
          {{ stat.value }}
        </p>
        <p class="mt-1 text-xs text-default-500">{{ stat.label }}</p>
      </div>
    </section>

    <!-- Quickstart -->
    <section>
      <div class="mb-10 text-center">
        <h2 class="text-2xl font-bold text-foreground md:text-3xl">三步开始</h2>
        <p class="mt-2 text-default-500">从登录到第一个成功请求,只需几分钟。</p>
      </div>
      <div class="grid gap-4 md:grid-cols-3">
        <KunCard
          v-for="(step, i) in quickstart"
          :key="step.title"
          :is-hoverable="false"
          content-class="justify-start gap-0 items-start"
          class-name="p-6 h-full"
        >
          <div class="flex items-center gap-3">
            <div
              class="flex size-10 items-center justify-center rounded-lg bg-primary-50 text-primary"
            >
              <KunIcon :name="step.icon" class="size-5" />
            </div>
            <span class="text-sm font-bold text-default-300">
              0{{ i + 1 }}
            </span>
          </div>
          <h3 class="mt-4 text-base font-semibold text-foreground">
            {{ step.title }}
          </h3>
          <p class="mt-1 text-sm leading-relaxed text-default-500">
            {{ step.body }}
          </p>
        </KunCard>
      </div>
    </section>

    <!-- Faces -->
    <section>
      <div class="mb-10 text-center">
        <h2 class="text-2xl font-bold text-foreground md:text-3xl">
          两个数据面
        </h2>
        <p class="mt-2 text-default-500">
          一份凭证覆盖全部;权限范围(scope)按面表达。
        </p>
      </div>
      <div class="grid gap-4 md:grid-cols-2">
        <NuxtLink
          v-for="face in faces"
          :key="face.path"
          to="/docs"
          class="group"
        >
          <KunCard
            :is-hoverable="true"
            content-class="justify-start gap-0 items-start"
            class-name="p-6 h-full"
          >
            <div class="flex w-full items-center justify-between">
              <div
                class="flex size-11 items-center justify-center rounded-lg bg-default-100 text-foreground"
              >
                <KunIcon :name="face.icon" class="size-5" />
              </div>
              <KunIcon
                name="lucide:arrow-up-right"
                class="size-4 text-default-300 transition-colors group-hover:text-primary"
              />
            </div>
            <div class="mt-4 flex items-center gap-2">
              <h3 class="text-base font-semibold text-foreground">
                {{ face.name }}
              </h3>
              <code class="font-mono text-xs text-default-400">{{
                face.path
              }}</code>
            </div>
            <p class="mt-1 text-sm leading-relaxed text-default-500">
              {{ face.body }}
            </p>
          </KunCard>
        </NuxtLink>
      </div>
    </section>

    <!-- Built for production -->
    <section>
      <div class="mb-10 text-center">
        <h2 class="text-2xl font-bold text-foreground md:text-3xl">
          为生产就绪而设计
        </h2>
        <p class="mt-2 text-default-500">聚合、鉴权、限流、契约 —— 一次到位。</p>
      </div>
      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div
          v-for="feature in features"
          :key="feature.title"
          class="rounded-2xl border border-default-200 bg-content1 p-6"
        >
          <div
            class="flex size-10 items-center justify-center rounded-lg bg-primary-50 text-primary"
          >
            <KunIcon :name="feature.icon" class="size-5" />
          </div>
          <h3 class="mt-4 text-base font-semibold text-foreground">
            {{ feature.title }}
          </h3>
          <p class="mt-1 text-sm leading-relaxed text-default-500">
            {{ feature.body }}
          </p>
        </div>
      </div>
    </section>

    <!-- Closing CTA -->
    <section
      class="rounded-2xl border border-default-200 bg-content1 px-6 py-12 text-center md:px-10"
    >
      <h2 class="text-2xl font-bold text-foreground md:text-3xl">
        几分钟内发出第一个请求
      </h2>
      <p class="mx-auto mt-3 max-w-xl text-default-500">
        登录生态账号,创建应用,领取密钥 —— 然后带上它请求任意公开面。
      </p>
      <div class="mt-7 flex flex-wrap items-center justify-center gap-3">
        <KunButton
          v-if="auth.isLoggedIn.value"
          color="primary"
          size="lg"
          @click="navigateTo('/dashboard')"
        >
          进入控制台
          <KunIcon name="lucide:arrow-right" class="ml-1 size-4" />
        </KunButton>
        <KunButton v-else color="primary" size="lg" @click="openLogin()">
          登录开始
          <KunIcon name="lucide:arrow-right" class="ml-1 size-4" />
        </KunButton>
        <KunButton variant="flat" size="lg" @click="navigateTo('/docs')">
          查看 API 文档
        </KunButton>
      </div>
    </section>
  </div>
</template>
