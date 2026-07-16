<script setup lang="ts">
import { API_BASE_URL } from '~/constants/dev'

const auth = useAuth()

useSeoMeta({
  title: '开发者平台',
  description:
    '统一 VNDB / Bangumi / DLsite / ErogameScape 四源的 galgame 数据 API。注册应用、领取密钥、阅读文档，几分钟内发出第一个请求。',
  ogTitle: 'NextMoe 开放 API 开发者平台',
  ogDescription: '统一四源的 galgame 数据 API — 一个 base URL，一份凭证。'
})

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
    body: '聚合记录:多源归并的名称 / 简介 / 封面 / 标签 / 会社 / 发售 / 三源评分,含变更流增量同步。'
  },
  {
    icon: 'lucide:network',
    name: 'Catalog 面',
    path: '/v1/catalog',
    body: '跨媒介身份图谱:作品 / 人物名义 / 角色 / 厂牌 / credits / 关系,外部 id 反查四源锚。'
  }
]

const curlSample = `curl https://api.nextmoe.dev/v1/galgame/1 \\
  -H "Authorization: Bearer nm_live_…"`
</script>

<template>
  <div class="space-y-20">
    <!-- Hero -->
    <section class="pt-6 text-center md:pt-12">
      <div
        class="inline-flex items-center gap-2 rounded-full border border-default-200 bg-content1 px-3 py-1 text-xs font-medium text-default-500"
      >
        <span class="size-2 rounded-full bg-success" />
        Phase 1 · 公开只读 API
      </div>

      <h1
        class="mx-auto mt-6 max-w-3xl text-4xl font-bold tracking-tight text-foreground md:text-5xl md:leading-tight"
      >
        统一四源的 galgame 数据 API
      </h1>
      <p class="mx-auto mt-2 text-sm font-medium tracking-wide text-primary">
        One base URL. One credential. Every source.
      </p>
      <p class="mx-auto mt-5 max-w-2xl text-lg text-default-500">
        把 VNDB、Bangumi、DLsite、ErogameScape 归并成一份稳定的聚合记录,
        再加上跨媒介的人物、角色与作品关系图谱 —— 通过一个 base URL 与一份凭证访问。
      </p>

      <div class="mt-8 flex flex-wrap items-center justify-center gap-3">
        <KunButton
          v-if="auth.isLoggedIn.value"
          color="primary"
          size="lg"
          @click="navigateTo('/dashboard')"
        >
          进入控制台
          <KunIcon name="lucide:arrow-right" class="ml-1 size-4" />
        </KunButton>
        <KunButton
          v-else
          color="primary"
          size="lg"
          @click="navigateTo('/login')"
        >
          登录开始
          <KunIcon name="lucide:arrow-right" class="ml-1 size-4" />
        </KunButton>
        <KunButton variant="flat" size="lg" @click="navigateTo('/docs')">
          <KunIcon name="lucide:book-open" class="mr-1 size-4" />
          查看 API 文档
        </KunButton>
      </div>

      <!-- Base URL + sample -->
      <div class="mx-auto mt-10 max-w-2xl text-left">
        <div
          class="flex items-center justify-between gap-3 rounded-t-xl border border-default-200 bg-content1 px-4 py-2"
        >
          <span class="text-xs font-medium text-default-400">Base URL</span>
          <div class="flex items-center gap-2">
            <span class="font-mono text-sm text-foreground">{{
              API_BASE_URL
            }}</span>
            <KunCopy :text="API_BASE_URL" size="sm" />
          </div>
        </div>
        <pre
          class="overflow-x-auto rounded-b-xl border border-t-0 border-default-200 bg-default-50 px-4 py-3 font-mono text-xs leading-relaxed text-default-600"
        ><code>{{ curlSample }}</code></pre>
      </div>
    </section>

    <!-- Quickstart -->
    <section>
      <div class="mb-8 text-center">
        <h2 class="text-2xl font-bold text-foreground">三步开始</h2>
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
      <div class="mb-8 text-center">
        <h2 class="text-2xl font-bold text-foreground">两个数据面</h2>
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

    <!-- Auth note -->
    <section
      class="rounded-2xl border border-default-200 bg-content1 px-6 py-8 md:px-10"
    >
      <div class="grid gap-6 md:grid-cols-3">
        <div>
          <h3 class="flex items-center gap-2 text-sm font-semibold text-foreground">
            <KunIcon name="lucide:shield-check" class="size-4 text-primary" />
            鉴权
          </h3>
          <p class="mt-2 text-sm text-default-500">
            服务端持 API Key,以
            <code class="font-mono text-xs text-foreground">
              Authorization: Bearer nm_live_…
            </code>
            发送。密钥是机密,仅服务端使用。
          </p>
        </div>
        <div>
          <h3 class="flex items-center gap-2 text-sm font-semibold text-foreground">
            <KunIcon name="lucide:gauge" class="size-4 text-primary" />
            限流与配额
          </h3>
          <p class="mt-2 text-sm text-default-500">
            按 tier 分层的每分钟限流与每日配额,响应头携带剩余额度。公开读经
            Cloudflare 边缘缓存。
          </p>
        </div>
        <div>
          <h3 class="flex items-center gap-2 text-sm font-semibold text-foreground">
            <KunIcon name="lucide:git-branch" class="size-4 text-primary" />
            稳定契约
          </h3>
          <p class="mt-2 text-sm text-default-500">
            URL 版本化 <code class="font-mono text-xs text-foreground">/v1</code>;已发布字段只做向后兼容的新增,破坏性变更升 <code
              class="font-mono text-xs text-foreground"
              >/v2</code
            >。
          </p>
        </div>
      </div>
    </section>
  </div>
</template>
