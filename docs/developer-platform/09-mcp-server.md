# MCP Server(公开只读面的 AI-agent 协议适配层)

> 拍板 2026-07-23(D4 提级 Phase 2,见 [01 §13](./01-design.md);派发-验收模式同 wave 08/09)。
> 一句话定位:把**已有的公开 /v1 只读面**同时暴露为 **MCP(Model Context
> Protocol)server**,让 AI 助手 / agent 用自然的工具调用直接查生态目录——
> **不新造任何数据面、权限面或计量面**。

## 1. 核心架构裁决:纯透传适配器(thin pass-through adapter)

MCP server 是公开 /v1 契约前面的一层**协议适配**,不是第二个 API:

- 每个 MCP tool 调用 = 一次对公开 /v1 面(`api.nextmoe.dev`)的 HTTP 请求,
  **原样转发调用方的 API key**;响应 JSON 即 tool result。
- 因此鉴权、tier、NSFW 可见性、限流、日配额、用量计量**全部天然复用**:
  流量落在同一个面、记在同一把 key 上(`/dev/usage` 里与直连流量无别)。
  MCP 层自身**零 authz 逻辑、零计量逻辑**——它连数据库都不碰。
- 面的版本化留在上游 `/v1`;tool 名不带版本。上游 expand→contract 纪律
  (02 §3.5)自动覆盖 MCP 消费者。

## 2. 形态与宿主

- 新 Go 服务 **`cmd/mcp`**(端口 **9285**),用 **官方 MCP Go SDK**
  (`github.com/modelcontextprotocol/go-sdk`;执行时核最新 tag,若官方 SDK
  能力缺口再评估 `mark3labs/mcp-go`,以官方优先)。
- Transport:**Streamable HTTP、stateless 模式**(无会话粘性,水平扩展与
  现有 Fiber 服务同治)。不做 stdio 分发(自托管用户 M2 再议)。
- 域名:**`mcp.nextmoe.dev`**(独立子域,Dokploy panel-domain 姿态,与
  developer-portal 同款单服务项目;`nextmoe.dev` 族命名约定见 README)。
- 上游 base 走 env(`KUN_MCP_UPSTREAM_BASE`,prod = api.nextmoe.dev 的
  内网服务地址),超时/重试保守(单次 30s,不重试非幂等——M1 全只读,
  幂等 GET 允许一次重试)。

## 3. 认证(M1)

- 调用方在 MCP endpoint 上带 `Authorization: Bearer nm_<api-key>`(各 MCP
  客户端的标准 header 配置即可)。MCP 层只做**形态检查**(缺失/非 `nm_`
  前缀 → 立即 MCP error,提示去 developer.nextmoe.dev 领 key);**真正的
  鉴权仍在上游面**(key 无效/超限时把面的 401/403/429 错误体转成带
  说明的 tool error 返回)。
- MCP 规范的 OAuth 2.1 授权流 = M2(第三方实际开放后,与 `dev:manage`
  同期评估);M1 的静态 key 模式对 agent 场景已充分。

## 4. 工具面(11 个 = M1 七个 + `catalog_name_get` + canonical-W1 三件;2026-07-28 与 104-108 波 spec 同步)

| tool | 上游端点 | 说明 |
|---|---|---|
| `galgame_search` | `GET /v1/galgame/search` | Meili 全文搜 galgame(+`fields`/`content_limit`/`age_limit`/`released_months` 透传) |
| `galgame_get` | `GET /v1/galgame/{id}` | 详情(携 `catalog_work_id` 跨面互链;+`fields`/`content_limit`) |
| `catalog_search` | `GET /v1/catalog/search` | 实体搜索,`type=names\|characters\|labels\|works`(works=跨媒介作品标题,r18 需 `nsfw=true`) |
| `catalog_work_get` | `GET /v1/catalog/works/{id}` | 注册行 + 可选 credits/relations(`include=credits,relations` 由该端点单次内联返回——MCP 层纯透传 `include`,不再并取子端点;+`nsfw`) |
| `catalog_lookup_external` | `GET /v1/catalog/lookup` | killer:`source=vndb&external_id=v19658` → work + 认领指针(+`nsfw`,默认 r18 命中 404) |
| `catalog_name_get` | `GET /v1/catalog/names/{id}` | 名义(credit-name 同人格分组;`include=credits` 附署名作品+角色) |
| `catalog_label_get` | `GET /v1/catalog/labels/{id}` | 厂牌/社团(intros[]/links[];`include=works`+`nsfw`) |
| `catalog_character_get` | `GET /v1/catalog/characters/{id}` | 角色(traits 按 `spoilers=0-2` 分级;`nsfw` 控 r18 作品+sexual 系 traits) |
| `catalog_works_list` | `GET /v1/catalog/works` | 批量浏览/过滤(content_rating/claimed/label/tag/series/platform/发售窗;`ids=` 批量水合;keyset 分页) |
| `catalog_changes` | `GET /v1/catalog/changes` | 增量同步变更流(keyset 游标存续轮询;entity_type=work) |
| `catalog_tag_get` | `GET /v1/catalog/tags/{id}` | 正典标签(跨源标签词表;`include=works` 附携带作品) |

- **catalog 覆盖面(9/12,三条「有意留白」)**:公开 catalog 面共 12 op,上表覆盖 9;
  留白 `POST /v1/catalog/lookup/batch`(批量外部 id 水合)、`GET /v1/catalog/redirects`
  (合并事件 keyset 流,供镜像清理存量 id)、`POST /v1/catalog/resolve`(旧 id→正典 id
  批量扁平化)。这三条服务的是**镜像维护 / 批量同步**型消费者,应直连 HTTP 面——单轮
  LLM tool call 没有批量、也没有存量 id 维护语义;小批量水合已由 `catalog_works_list`
  的 `ids=` 覆盖,单个外部 id 由 `catalog_lookup_external` 覆盖;且 `lookup/batch` 与
  `resolve` 是 POST,而 mcpface 传输是 GET 纯透传。

- **r18 姿态(104 波,调用方自控)**:catalog 系工具 `nsfw=true` 显式开;galgame 系
  `content_limit=sfw|nsfw|all`(需 key 带 `galgame:nsfw` scope,否则静默降 sfw)。
  默认全部隐藏——LLM 消费者不显式要就永远看不到 r18。

- tool description 用英文、面向 LLM 写清「何时用哪个」(lookup vs search
  的分工是重点:有外部 id 用 lookup,自然语言用 search)。
- 输入 schema 逐参对齐上游 query 参数(分页参数透传,默认页量保守)。
- **不做**的(明确出界):calendar 流(galgame 面,agent 场景弱)、
  redirects/resolve/lookup/batch(镜像维护面,理由见上面的覆盖说明)、
  resources/prompts(M2)、任何写面(Phase 3 submit 开放后随 OAuth 一起
  评估)。`changes` 原属此列,canonical-W1 已进面(见上表)。

## 5. 运维与部署

- 独立 Dokploy 单服务项目(照抄 developer-portal 的 panel-domain +手动
  Deploy 姿态,`docker-compose.mcp.yml`);镜像走现有 CI 矩阵。
- healthz 照平台惯例;结构化日志记 tool 名 + 上游状态码 + 时延,
  **永不记 key 明文**(fingerprint 前 8 hex)。
- 冒烟:MCP `initialize` + `tools/list` + 一次 `galgame_search` 真调用。(2026-07-28 同步后 `tools/list` 应回 11 工具。)

## 6. 阶段

- **M1(本波)**:§1-§5 全部;门户 docs 页加「AI/MCP 接入」一节(端点、
  key 配置示例:Claude Code / Claude Desktop / 通用 MCP 客户端片段)。
- **M2(触发式)**:MCP resources(work 页面作为资源)、prompts、OAuth
  2.1、stdio 自托管包、写面工具。触发条件 = 真实外部消费者出现。
