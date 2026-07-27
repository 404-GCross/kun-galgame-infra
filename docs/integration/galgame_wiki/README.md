# galgame 面 · 桥面读退役 + 平台工作流面参考

> **⚠️ 两段历史,读清楚再用。**
>
> 这个面(catalog 服务的 `/internal/*`,devapi `nm_` key)诞生时是**开放 API Phase 2 路线 B** 的过渡桥。
> **路线 B 终局已在 W5(2026-07-22)落地**,`/internal` 读面一分为二:
>
> 1. **公开数据读 = 已退役 → `/v1/galgame/*`。** 列表 / 详情 / batch / 搜索 / 月历 / stats / check / 关系反查 + 四族 taxonomy 的 list/search/by-id —— 共 **29 条 A/C 桶读** —— 全部下线,下游改从**冻结的公开 `/v1` 契约**消费同一份数据(W1-W4)。这些桥路由现在返回 `404`(路径已无)或 `405`(路径被同名写路由占用,仅 GET 读退役)。
> 2. **平台工作流面 = 留任(不是遗产,是设计面)。** 剩 **15 条 B 桶路由** + 2 条 S2S feed,W5 显式**重新立册(re-charter)**为**平台工作流面**——与 `/internal` 写面(06a)、提案面(06b)同族的双凭证工作流面,curated `/v1` 不该也无法承载它。它**按宪章永久留任**,face 计量续用 `galgame_internal`。

本目录曾是「Galgame Wiki API」的整套人读契约手册。**wiki 前端 / `wiki.kungal.com` 域 / 独立 galgame 服务 / legacy `/api` 读面 / Basic-auth feeds / `*_WIKI_BASE_URL` env 名**均已在**开放 API Phase 2 · W5(2026-07-21)**退役;**桥面的公开数据读**又在**路线 B · W5(2026-07-22)**退役到 `/v1`。故整套手册已失真,收容为本页参考。

---

## 面与基址

| 项 | 值 |
|---|---|
| 服务 | catalog(container `catalog`,端口 `9281`) |
| 基址 env | `KUN_NEXTMOE_API_BASE`(prod `http://catalog:9281`,dev 默认 `http://127.0.0.1:19281`) |
| 平台工作流面前缀 | `/internal`(客户端在基址之后拼 `/internal/...`) |
| 公开数据面前缀 | `/v1/galgame`(**桥面 A/C 桶读的新家**——见下「已退役」) |
| S2S feeds | `GET /internal/galgame/messages/feed`、`GET /internal/galgame/revisions/recent`(W5-05 由 legacy `/api` + Basic 迁入本面;路线 B W5 按宪章留任) |

> legacy `/api` 前缀的 galgame **读**路由已在 W5-05 退役;`/api` 现只承载**写 / 投稿 / staff / 图片上传 / taxonomy CRUD·revert**面(06 波领地,永久 S2S,不进本参考)。`/internal` 上的**用户写面**(06a)与**提案面**(06b `/internal/edit/*`)是本工作流面的兄弟面,同样不在本读参考的路由集内。

## 鉴权(硬依赖 key,无回退)

- 头:`X-API-Key: nm_...`
- key 需 **internal tier** + scope **`galgame:read`**;流量计量于 `galgame_internal`(工作流面与已退役的桥读**共用同一计量 face**,故计量口径连续)。
- **无** OAuth-client Basic、**无** 匿名读:key 即身份。无效 / 缺失 key → `401`;tier 不足 → `403`。
- `/mine`、`/messages/mine` 额外要求终端用户 **Bearer JWT**(双凭证:`X-API-Key`=客户端身份,`Authorization: Bearer`=用户令);`/search` 的 `include_pending` 走 optionalJWT(有 JWT 才出本人 pending)。

> **回退阀已死。** 配了 `KUN_NEXTMOE_API_BASE` 却把 `KUN_NEXTMOE_API_KEY` 留空 = **启动 fail-fast**(下游 forum/patch/letmoe 均已改为硬依赖),**不是**静默回落到旧的无鉴权 legacy `/api`。旧 env 名 `GALGAME_WIKI_BASE_URL` / `KUN_GALGAME_WIKI_BASE_URL` / `KUN_WIKI_API_BASE` 已全部退役。

## 响应格式

统一信封,分页 `data` 为 `{ items, total }`:

```json
{ "code": 0, "message": "成功", "data": { "items": [], "total": 0 } }
```

## 已退役 → 迁 `/v1/galgame`(路线 B W5,29 条 A/C 桶读)

以下桥读**已从 `/internal` 下线**。下游改调 `/v1/galgame` 的**冻结公开契约**(curated 形状,非桥面 raw-model;富化走 include 词表)。桥面 raw 形状的 legacy 怪癖(engine bare-array、`/:name` 从 query 取 id、raw GORM 行泄漏内部列)**刻意不带入** `/v1`。

| 桥面(已 404/405) | `/v1` 新址 |
|---|---|
| `GET /internal/galgame/`(bridge list,零消费 C 桶) | 无 analog(随桥退役) |
| `GET /internal/galgame/batch` | `GET /v1/galgame/batch`(`include=meta` 富化) |
| `GET /internal/galgame/:gid`(详情) | `GET /v1/galgame/{id}`(`include=intro,taxonomy,meta,links,covers,screenshots,series,contributors,...`) |
| `GET /internal/galgame/:gid/links`·`/aliases`·`/scores` | `GET /v1/galgame/{id}?include=links` / `…`(detail 子块) |
| `GET /internal/galgame/:gid/contributors`(零消费 C 桶) | `GET /v1/galgame/{id}?include=contributors`(W1d 加性) |
| `GET /internal/galgame/check` | `GET /v1/galgame/lookup?vndb_id=` |
| `GET /internal/galgame/stats` | `GET /v1/galgame/stats` |
| `GET /internal/galgame/calendar{,/pending,/tba}` | `GET /v1/galgame/calendar{,/pending,/tba}`(parity 面) |
| `GET /internal/galgame/officials\|tags/:id/galgames` | `GET /v1/galgame/officials\|tags/{id}/galgames` |
| `GET /internal/tag{,/search,/multi,/:name,/:id/galgame-ids}` | `GET /v1/galgame/tags{,/search,/multi,/{id},/{id}/galgame-ids}` |
| `GET /internal/official{,/search,/:name,/:id/galgame-ids}` | `GET /v1/galgame/officials{,/search,/{id},/{id}/galgame-ids}` |
| `GET /internal/engine{,/:name,/:id/galgame-ids}` | `GET /v1/galgame/engines{,/{id},/{id}/galgame-ids}` |
| `GET /internal/series{,/:id}`(+ `/search` 由 `/v1` search 适配,零消费 C 桶) | `GET /v1/galgame/series{,/{id}}` |

> `/v1` 是**唯一公开数据契约**,契约 dogfooding(下游与第三方消费同一契约)。字段级真值取门户发布的 `public-openapi.yaml`(`docs/galgame_wiki/public-openapi.yaml`,code-first 从 Huma 导出)。NSFW = scope 门三态 `content_limit=sfw|nsfw|all`(需 key 持 `galgame:nsfw` scope,否则静默回落 sfw)。
>
> **标签层级扩展(2026-07 加性)**:`GET /v1/galgame/tags/multi` 新增可选参数 **`expand=descendants`** —— 每个请求 id 先展开为「自身 + 其层级后代」,一部游戏在**每一组**里命中至少一个标签即入选(组间 AND、组内 OR),单次查询,total/分页精确;不传该参数 = 冻结的扁平 AND 交集,逐字节向后兼容。配套地 `GET /v1/galgame/tags/{id}` 详情新增 **`children`** 块(仅当有子标签时出现):直接子标签的 `{id, name, category, galgame_count}`,供 UI 呈现「展开将包含:硬科幻、科幻奇幻」。层级边由 VNDB 标签 DAG 投影到 wiki 词表(infra `cmd/backfill-tag-edges`);元分组节点(如 "Type")永不成为父节点,故「恋爱」的展开不会命中「无恋爱剧情」。

## 平台工作流面(留任 · 15 读 + 2 feed · 真值在代码)

W5 后 `/internal` 承载的**唯一读集** = 下列平台工作流路由。它**不是** wiki 遗产,是与写/提案面同族的设计面(见 infra `apps/api/internal/galgameapp/workflowroutes.go` 的章程注释)。字段级形状取代码。

**galgame 工作流(7)**

| 路由 | 章程 |
|---|---|
| `GET /internal/galgame/mine`(jwtAuth) | JWT 个人面:登录作者本人的投稿列表 |
| `GET /internal/galgame/messages/mine`(jwtAuth) | JWT 个人面:本人的审核消息 |
| `GET /internal/galgame/search`(optionalJWT) | **SearchWithPending**:kungal 发布向导消费的 **raw Meilisearch 文档透传**(完整 `GalgameDoc`+`_formatted`);curated `/v1` search 是投影形状,架构上无法逐字节复现整个 Meili doc,故按 **P1 修正留 B 桶**。optionalJWT 让登录者附带看到本人 pending/declined 草稿 |
| `GET /internal/galgame/drafts` | 社区工作流:草稿库(与 06a create/claim/submit 同族) |
| `GET /internal/galgame/user/:id/stats` | 社区工作流:用户贡献统计 |
| `GET /internal/galgame/user/:id/galgames` | 社区工作流:用户创建的 galgame |
| `GET /internal/galgame/user/:id/contributed` | 社区工作流:用户参与贡献的 galgame |

**taxonomy 修订(8)** —— legacy 形状历史,编辑引擎轨的未来疆域,**刻意不冻进 `/v1`**(其形状将在编辑引擎轨现代化)。

| 路由 | 章程 |
|---|---|
| `GET /internal/tag/:id/revisions`、`/internal/tag/:id/revisions/:rev` | tag 修订历史 |
| `GET /internal/official/:id/revisions`、`…/:rev` | official 修订历史 |
| `GET /internal/engine/:id/revisions`、`…/:rev` | engine 修订历史 |
| `GET /internal/series/:id/revisions`、`…/:rev` | series 修订历史 |

**S2S feeds(2 · 留任 · 注册在别处)** —— 机器同步原语,注册在 `devapiface.go` 的 `mountInternal`(不经工作流注册器):

- `GET /internal/galgame/messages/feed`
- `GET /internal/galgame/revisions/recent`

> **代码(单一真源)**:infra `apps/api/internal/galgameapp/`——`workflowroutes.go` = 15 工作流读的注册 + 逐路由章程;`devapiface.go` = `/internal` 面 devapi 链 + 2 feed 挂载;`writeroutes.go`(06a 写面)/ `proposeroutes.go`(06b 提案面)= 兄弟面。
>
> **机器可读 spec 注**:wiki 时代的机器可读 spec(`docs/galgame_wiki/read-openapi.yaml`、`calendar-openapi.yaml`,及门户发布的 `galgame-wiki.openapi.yaml` / `galgame-wiki-calendar.openapi.yaml`)已随桥面一同退役(W5)。平台工作流面**不再产出机器可读 spec**——与其兄弟 `/internal` 写面(06a)、提案面(06b)一致,二者本就无 spec;本面之真源即**代码(`workflowroutes.go`)+ 本页**。唯一对外的机器可读契约是门户发布的 `/v1` 公开投影 `docs/galgame_wiki/public-openapi.yaml`。

## 终态(路线 B 已达成)

`/v1` 富化(05-open-api step 07 / W1a-d)已落地,下游 A 桶读全迁 `/v1` 同形公开契约(W2-W4),桥面公开数据读随之退役(W5)。平台展示的公开 API = 真实 `/v1`;`/internal` 仅余**平台工作流面**(留任,永久 S2S)。第三方实际开放(含 nsfw scope 发放)是独立的用户级决策,不在本轨。
