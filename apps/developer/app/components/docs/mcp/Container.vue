<script setup lang="ts">
import { MCP_ENDPOINT } from '~/constants/dev'

useSeoMeta({
  title: 'AI / MCP 接入',
  description:
    '把 NextMoe 开放 API 作为 MCP（Model Context Protocol）server 接入 AI 助手：端点、密钥配置，以及 Claude Code / Claude Desktop / 通用 MCP 客户端三段配置示例。'
})

const tools = [
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
  },
  {
    name: 'catalog_works_list',
    desc: '批量浏览 / 过滤作品注册表（评级 / 厂牌 / 标签 / 系列 / 平台 / 发售窗，keyset 分页，ids= 批量水合）。'
  },
  {
    name: 'catalog_changes',
    desc: '增量同步变更流——存下 next_cursor，下次轮询只拿变化的部分。'
  },
  {
    name: 'catalog_tag_get',
    desc: '按 id 取正典标签（跨源标签词表），include=works 附携带作品。'
  },
  {
    name: 'catalog_works_search',
    desc: '作品产品检索：自由文本 + works-list 全过滤集，五档排序、可选 facets 分面计数、page 分页（组合「查询 + 过滤」时优先用它，纯名字检索用 catalog_search）。'
  },
  {
    name: 'catalog_calendar',
    desc: '发售月历单月（缺省为当前 Asia/Tokyo 月；olang 缺省收敛到 ja + zh* 族，olang=all 放开）。'
  },
  {
    name: 'catalog_calendar_pending',
    desc: '月历「知年不知月」桶（缺省为当前 Asia/Tokyo 年）。'
  },
  {
    name: 'catalog_calendar_tba',
    desc: '月历「已公布未定档」全局桶。'
  },
  {
    name: 'catalog_labels_list',
    desc: '浏览厂牌 / 社团词表本身（kind 过滤，每行带 nsfw 感知 work_count）——用来发现 label id。'
  },
  {
    name: 'catalog_tags_list',
    desc: '浏览正典标签词表本身（tier / kind 过滤）——用来发现 tag id 再喂给作品过滤。'
  },
  {
    name: 'catalog_engines_list',
    desc: '浏览引擎词表本身——用来发现 engine id 再喂给 catalog_works_search。'
  },
  {
    name: 'catalog_engine_get',
    desc: '按 id 取引擎记录（名称 + nsfw 感知 work_count + 跨源 refs）。'
  },
  {
    name: 'catalog_series_list',
    desc: '浏览系列词表本身（source= 泳道过滤：curated / derived / dlsite，每行带 nsfw 感知 work_count）——系列不进搜索索引，这是发现 series id 的唯一入口。'
  },
  {
    name: 'catalog_series_get',
    desc: '按 id 取系列（身份 + 源锚 + 简介），include_works 附成员作品并按阅读顺序排列——回答「这个系列按什么顺序玩」。'
  },
  {
    name: 'catalog_stats',
    desc: '全库计数：各媒介 LIVE 作品数 + 身份家族总量（无参数）。'
  },
  {
    name: 'catalog_label_relation_graph',
    desc: '一次拿到一个厂牌周围的整个会社家族（母公司 / 子品牌 / 文库 / 继承），nodes[] + edges[]。catalog_label_get 的 relations[] 只有一跳，问「某社旗下有哪些牌子」用这个。服务端封顶 depth 4 / 60 节点，不分页。'
  },
  {
    name: 'catalog_releases',
    desc: '发售动态的 release 粒度：每一条发售行各自成项，移植版 / 复刻 / 中文化都看得见（calendar 只把作品放在最早发售月且只显示一次）。可按日期区间、平台、发行语言、版本类型、官方性过滤；is_first 分辨首发与再版。'
  }
]

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

    <section>
      <h2 class="text-lg font-semibold text-foreground">工具面（{{ tools.length }} 个）</h2>
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
