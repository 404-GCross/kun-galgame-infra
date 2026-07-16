<script setup lang="ts">
import { API_BASE_URL } from '~/constants/dev'
import { DOCS_FACE_META } from '~/constants/docs'

// /docs landing: what the API is, how to authenticate, and the two face entries.
const { faces, faceOperationCount } = useDocs()

useSeoMeta({
  title: 'API 文档',
  description:
    'NextMoe 开放 API 参考文档：galgame 聚合面与 catalog 身份图谱面，共 19 个公开只读端点。鉴权、限流与每个端点的参数 / 响应 / curl 示例。'
})

const totalOperations = computed(() =>
  faces.reduce((n, f) => n + faceOperationCount(f), 0)
)
</script>

<template>
  <div class="space-y-10">
    <!-- Intro -->
    <header>
      <p class="text-sm font-medium tracking-wide text-primary">API 参考</p>
      <h1 class="mt-2 text-3xl font-bold tracking-tight text-foreground">
        NextMoe 开放 API
      </h1>
      <p class="mt-3 max-w-2xl text-default-500">
        统一 VNDB / Bangumi / DLsite / ErogameScape 四源的 galgame 数据。
        一个 base URL、一份凭证，覆盖 {{ totalOperations }} 个公开只读端点，分两个数据面。
      </p>
    </header>

    <!-- Base URL + auth + rate limit -->
    <section class="grid gap-4 md:grid-cols-3">
      <div class="rounded-xl border border-default-200 bg-content1 p-4">
        <h2 class="flex items-center gap-2 text-sm font-semibold text-foreground">
          <KunIcon name="lucide:link" class="size-4 text-primary" />
          Base URL
        </h2>
        <div class="mt-3 flex items-center justify-between gap-2">
          <code class="min-w-0 flex-1 truncate font-mono text-sm text-foreground">
            {{ API_BASE_URL }}
          </code>
          <DocsCopyButton :text="API_BASE_URL" label="复制 base URL" />
        </div>
      </div>

      <div class="rounded-xl border border-default-200 bg-content1 p-4">
        <h2 class="flex items-center gap-2 text-sm font-semibold text-foreground">
          <KunIcon name="lucide:shield-check" class="size-4 text-primary" />
          鉴权
        </h2>
        <p class="mt-3 text-sm text-default-500">
          每个请求携带
          <code
            class="rounded bg-default-100 px-1 py-0.5 font-mono text-xs text-foreground"
          >
            Authorization: Bearer nm_live_…
          </code>
          。密钥是机密，仅服务端持有。
        </p>
      </div>

      <div class="rounded-xl border border-default-200 bg-content1 p-4">
        <h2 class="flex items-center gap-2 text-sm font-semibold text-foreground">
          <KunIcon name="lucide:gauge" class="size-4 text-primary" />
          限流
        </h2>
        <p class="mt-3 text-sm text-default-500">
          按 tier 分层（free 60 次/分 · 50,000 次/日）。超限返回
          <code class="font-mono text-xs text-foreground">429</code>
          并携带
          <code class="font-mono text-xs text-foreground">X-RateLimit-*</code>
          头。
        </p>
      </div>
    </section>

    <!-- Face entries -->
    <section>
      <h2 class="text-lg font-semibold text-foreground">两个数据面</h2>
      <p class="mt-1 text-sm text-default-500">
        一份凭证覆盖全部；权限范围（scope）按面表达。
      </p>
      <div class="mt-4 grid gap-4 md:grid-cols-2">
        <NuxtLink
          v-for="face in faces"
          :key="face.key"
          :to="`/docs/${face.key}`"
          class="group rounded-xl border border-default-200 bg-content1 p-5 transition-colors hover:border-primary"
        >
          <div class="flex items-center justify-between">
            <div
              class="flex size-11 items-center justify-center rounded-lg bg-default-100 text-foreground"
            >
              <KunIcon :name="DOCS_FACE_META[face.key].icon" class="size-5" />
            </div>
            <span
              class="rounded-full bg-default-100 px-2.5 py-1 text-xs font-medium text-default-500"
            >
              {{ faceOperationCount(face) }} 端点
            </span>
          </div>
          <div class="mt-4 flex items-center gap-2">
            <h3 class="text-base font-semibold text-foreground">
              {{ face.label }} 面
            </h3>
            <code class="font-mono text-xs text-default-400">
              /v1/{{ face.key }}
            </code>
          </div>
          <p class="mt-1 text-sm leading-relaxed text-default-500">
            {{ DOCS_FACE_META[face.key].tagline }}
          </p>
          <span
            class="mt-4 inline-flex items-center gap-1 text-sm font-medium text-primary"
          >
            查看端点
            <KunIcon
              name="lucide:arrow-right"
              class="size-4 transition-transform group-hover:translate-x-0.5"
            />
          </span>
        </NuxtLink>
      </div>
    </section>
  </div>
</template>
