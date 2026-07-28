<script setup lang="ts">
import { MCP_ENDPOINT } from '~/constants/dev'

// AI / MCP access guide: how to point an AI assistant at the NextMoe catalog via
// the Model Context Protocol. The MCP server is a thin, stateless adapter over
// the SAME public /v1 read faces documented here — one key authenticates both.
useSeoMeta({
  title: 'AI / MCP 接入',
  description:
    '把 NextMoe 开放 API 作为 MCP（Model Context Protocol）server 接入 AI 助手：端点、密钥配置，以及 Claude Code / Claude Desktop / 通用 MCP 客户端三段配置示例。'
})

// The eight tools (mirrors apps/api/internal/platform/mcpface). Each maps 1:1
// to a public /v1 endpoint; lookup/get take an id, search takes natural language.
// R18 is hidden by default: nsfw=true on catalog tools, content_limit on galgame.
const tools = [
  {
    name: 'galgame_search',
    desc: '全文搜 galgame 聚合面，按相关度返回 brief 记录（自然语言查询首选；支持 fields 稀疏投影与 content_limit 内容分级）。'
  },
  {
    name: 'galgame_get',
    desc: '按 id 取一部 galgame 的完整聚合记录（含跨面互链 catalog_work_id）。'
  },
  {
    name: 'catalog_search',
    desc: '按名字搜身份图谱实体：names（人物名义）/ characters / labels / works（跨媒介作品标题，r18 需 nsfw=true）。'
  },
  {
    name: 'catalog_work_get',
    desc: '按 catalog work id 取注册行，include=credits,relations 并取子块。'
  },
  {
    name: 'catalog_lookup_external',
    desc: '外部 id 反查（如 source=vndb, external_id=v19658）——手握外部 id 时首选。'
  },
  {
    name: 'catalog_name_get',
    desc: '按 id 取名义（credit-name 同人格分组），include=credits 附署名作品与角色。'
  },
  { name: 'catalog_label_get', desc: '按 id 取厂牌 / 社团（include=works 附归属作品）。' },
  {
    name: 'catalog_character_get',
    desc: '按 id 取角色（traits 按 spoilers=0-2 分级；nsfw 控 r18 作品与 sexual 系 traits）。'
  }
]

// Client config snippets. The key is a secret — the placeholder stands in for
// the developer's real nm_live_… key, injected client-side, never shipped.
const claudeCodeCmd = `claude mcp add --transport http nextmoe ${MCP_ENDPOINT} \\
  --header "Authorization: Bearer nm_live_你的密钥"`

const claudeDesktopJson = `{
  "mcpServers": {
    "nextmoe": {
      "type": "http",
      "url": "${MCP_ENDPOINT}",
      "headers": {
        "Authorization": "Bearer nm_live_你的密钥"
      }
    }
  }
}`

const genericJson = `{
  "transport": "streamable-http",
  "url": "${MCP_ENDPOINT}",
  "headers": {
    "Authorization": "Bearer nm_live_你的密钥"
  }
}`

// A bare Streamable-HTTP handshake, proving the endpoint is a plain HTTP MCP
// server (no SDK required) — initialize → then tools/list, tools/call.
const curlHandshake = `curl -sN ${MCP_ENDPOINT} \\
  -H "Content-Type: application/json" \\
  -H "Accept: application/json, text/event-stream" \\
  -H "Authorization: Bearer nm_live_你的密钥" \\
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize",
       "params":{"protocolVersion":"2025-06-18","capabilities":{},
                 "clientInfo":{"name":"curl","version":"0"}}}'`
</script>

<template>
  <div class="space-y-10">
    <!-- Intro -->
    <header>
      <p class="text-sm font-medium tracking-wide text-primary">AI 接入</p>
      <h1 class="mt-2 text-3xl font-bold tracking-tight text-foreground">
        AI / MCP 接入
      </h1>
      <p class="mt-3 max-w-2xl text-default-500">
        NextMoe 开放 API 同时以 <strong class="text-foreground">MCP</strong>（Model
        Context Protocol）server 暴露：AI 助手 / agent 用自然的工具调用直接查生态目录，
        无需为它写胶水代码。它是一层<strong class="text-foreground">纯透传适配</strong>——
        每次工具调用就是一次对公开 /v1 面的请求，原样带上你的密钥。
      </p>
    </header>

    <!-- Endpoint + transport + auth -->
    <section class="grid gap-4 md:grid-cols-3">
      <div class="rounded-xl border border-default-200 bg-content1 p-4">
        <h2 class="flex items-center gap-2 text-sm font-semibold text-foreground">
          <KunIcon name="lucide:plug" class="size-4 text-primary" />
          端点（Endpoint）
        </h2>
        <div class="mt-3 flex items-center justify-between gap-2">
          <code class="min-w-0 flex-1 truncate font-mono text-sm text-foreground">
            {{ MCP_ENDPOINT }}
          </code>
          <DocsCopyButton :text="MCP_ENDPOINT" label="复制端点" />
        </div>
      </div>

      <div class="rounded-xl border border-default-200 bg-content1 p-4">
        <h2 class="flex items-center gap-2 text-sm font-semibold text-foreground">
          <KunIcon name="lucide:route" class="size-4 text-primary" />
          传输（Transport）
        </h2>
        <p class="mt-3 text-sm text-default-500">
          Streamable HTTP，
          <span class="text-foreground">stateless</span>（无会话粘性）。任何支持
          HTTP MCP 的客户端可直接接入，无需自托管。
        </p>
      </div>

      <div class="rounded-xl border border-default-200 bg-content1 p-4">
        <h2 class="flex items-center gap-2 text-sm font-semibold text-foreground">
          <KunIcon name="lucide:shield-check" class="size-4 text-primary" />
          鉴权
        </h2>
        <p class="mt-3 text-sm text-default-500">
          在端点上带
          <code
            class="rounded bg-default-100 px-1 py-0.5 font-mono text-xs text-foreground"
          >
            Authorization: Bearer nm_live_…
          </code>
          。与直连同一把密钥，
          <NuxtLink to="/dashboard" class="text-primary hover:underline">
            在控制台
          </NuxtLink>
          领取。
        </p>
      </div>
    </section>

    <!-- Key idea: zero new authz/metering -->
    <section
      class="flex items-start gap-3 rounded-xl border border-primary-200 bg-primary-50 p-4"
    >
      <KunIcon
        name="lucide:info"
        class="mt-0.5 size-5 shrink-0 text-primary"
      />
      <p class="text-sm leading-relaxed text-default-600">
        MCP 层自身<strong class="text-foreground">零鉴权、零计量</strong>逻辑——鉴权、tier、
        NSFW 可见性、限流、日配额与用量统计全部复用同一个面、记在同一把密钥上：一次工具调用在
        <code class="font-mono text-xs text-foreground">/dev/usage</code>
        里与一次直连 <code class="font-mono text-xs text-foreground">/v1</code> 请求毫无区别。
      </p>
    </section>

    <!-- Tool surface -->
    <section>
      <h2 class="text-lg font-semibold text-foreground">工具面（7 个）</h2>
      <p class="mt-1 text-sm text-default-500">
        每个工具映射一个公开只读端点。手握 id / 外部 id 用
        <code class="font-mono text-xs text-foreground">*_get</code> /
        <code class="font-mono text-xs text-foreground">*_lookup</code>，自然语言用
        <code class="font-mono text-xs text-foreground">*_search</code>。
      </p>
      <ul class="mt-4 space-y-2">
        <li
          v-for="tool in tools"
          :key="tool.name"
          class="flex flex-col gap-1 rounded-lg border border-default-200 bg-content1 p-3 sm:flex-row sm:items-center sm:gap-3"
        >
          <code
            class="w-fit shrink-0 rounded bg-default-100 px-2 py-1 font-mono text-xs font-medium text-foreground"
          >
            {{ tool.name }}
          </code>
          <span class="text-sm text-default-500">{{ tool.desc }}</span>
        </li>
      </ul>
    </section>

    <!-- Client configuration -->
    <section class="space-y-4">
      <div>
        <h2 class="text-lg font-semibold text-foreground">客户端配置</h2>
        <p class="mt-1 text-sm text-default-500">
          把下面的
          <code class="font-mono text-xs text-foreground">nm_live_你的密钥</code>
          换成你自己的密钥。
        </p>
      </div>

      <div class="space-y-2">
        <h3 class="text-sm font-semibold text-foreground">Claude Code（CLI）</h3>
        <DocsMcpConfigBlock
          label="终端"
          icon="lucide:terminal"
          :code="claudeCodeCmd"
        />
      </div>

      <div class="space-y-2">
        <h3 class="text-sm font-semibold text-foreground">
          Claude Desktop（claude_desktop_config.json）
        </h3>
        <DocsMcpConfigBlock label="JSON" icon="lucide:braces" :code="claudeDesktopJson" />
        <p class="text-xs text-default-400">
          较旧、尚不支持远程 HTTP server 的桌面版，可用
          <code class="font-mono text-foreground">npx mcp-remote</code>
          作为 stdio 桥接。
        </p>
      </div>

      <div class="space-y-2">
        <h3 class="text-sm font-semibold text-foreground">通用 MCP 客户端</h3>
        <DocsMcpConfigBlock label="JSON" icon="lucide:braces" :code="genericJson" />
      </div>
    </section>

    <!-- Raw handshake -->
    <section class="space-y-2">
      <h2 class="text-lg font-semibold text-foreground">裸 HTTP 握手</h2>
      <p class="text-sm text-default-500">
        端点是标准的 Streamable HTTP MCP server，无需 SDK 即可
        <code class="font-mono text-xs text-foreground">initialize</code> →
        <code class="font-mono text-xs text-foreground">tools/list</code> →
        <code class="font-mono text-xs text-foreground">tools/call</code>。
      </p>
      <DocsCurlBlock :code="curlHandshake" />
    </section>
  </div>
</template>
