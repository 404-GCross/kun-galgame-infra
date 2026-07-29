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

## 平台工作流面(留任 · 16 读 + 3 机器面读 · 真值在代码)

W5 后 `/internal` 承载的**唯一读集** = 下列平台工作流路由。它**不是** wiki 遗产,是与写/提案面同族的设计面(见 infra `apps/api/internal/galgameapp/workflowroutes.go` 的章程注释)。字段级形状取代码。

**galgame 工作流(8)**

| 路由 | 章程 |
|---|---|
| `GET /internal/galgame/mine`(jwtAuth) | JWT 个人面:登录作者本人的投稿列表 |
| `GET /internal/galgame/messages/mine`(jwtAuth) | JWT 个人面:本人的审核消息 |
| `GET /internal/galgame/search`(optionalJWT) | **SearchWithPending**:kungal 发布向导消费的 **raw Meilisearch 文档透传**(完整 `GalgameDoc`+`_formatted`);curated `/v1` search 是投影形状,架构上无法逐字节复现整个 Meili doc,故按 **P1 修正留 B 桶**。optionalJWT 让登录者附带看到本人 pending/declined 草稿 |
| `GET /internal/galgame/drafts` | 社区工作流:草稿库(与 06a create/claim/submit 同族) |
| `GET /internal/galgame/user/:id/stats` | 社区工作流:用户贡献统计 |
| `GET /internal/galgame/user/:id/galgames` | 社区工作流:用户创建的 galgame |
| `GET /internal/galgame/user/:id/contributed` | 社区工作流:用户参与贡献的 galgame |
| `GET /internal/galgame/taxonomy/{family}/search?q=`(jwtAuth) | **投稿表单的 taxonomy 选择器(A2-1g)**:`family` = `tag\|official\|engine\|series`,出 `{id,name}` 选择器行,**wiki id 空间**。词表外的 `family` = `400`(我方封闭词表,拼错是调用方错误,不是「无匹配」) |

> **为什么 taxonomy 选择器要有两扇门(A2-1g)**:投稿表单要把 tag / 会社 / 引擎 / 系列的**名字**解析成它写回载荷里带的 **wiki id**。A2-1e 区 B 已经给这个查询建了家,但挂在 `/api` staff 面的 `galgame.taxonomy.edit_any` 门后——于是**普通投稿者填表时被自己唯一能问的那条道 403**。
>
> 所以本 op = **同一个查询 + 投稿者门**。信任级取 `/internal` 写面 / 提案面既有的那一档(登录用户 + 双凭证),这不是放宽:它返回的只是公开 taxonomy 行的名字,而同一个用户下一步就要用这些 id 去**写**投稿;W5 之前那份数据在弃用的公开面上本来就是匿名可读的。
>
> **实现上只差一道门**:族词表、分词、行形状、条数上限全部来自共享的 `service.TaxonomyPicker`,所以前端同一个选择器组件指哪扇门都一样(测试直接对拍两扇门的响应体逐字节相同)。空 `q` 的行为按**族**而非按门区分:`tag`/`official`(3,037 / 24,334 行)返回空列表——请先打字;`engine`/`series`(189 / 146 行)返回整个小词表——两站本就当扁平列表水化。

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
- `GET /internal/galgame/meta?ids=…`(**A2-1e 加**)—— **归属元数据批取**:`{items:[{gid, user_id, status, name_zh_cn, name_zh_tw, name_ja_jp, name_en_us}]}`,**状态盲**、按 gid 升序、最多 100 id、无解 id 直接缺席(删了的条目不是错误)。**七键恒出**:未填的名是空串,消费端的 locale 回退链不必去区分「缺键」与「空值」。

> **为什么要有它**:forum 编辑轨的「谁能编辑」断言与「通知发给谁」目前都读**匿名的已发布 batch 面**——那个面对 status ∈ {2,3,4} 的条目**什么都不返回**,于是断言退化成「你不是作者」,真正的作者被挡在自己未发布条目的编辑 / 回滚 / 复核之外。本 op 就是这个问题的诚实供给:凭证面、状态盲。`status` 随行,是为了让调用方能区分「不是作者」与「未发布」,而不是从「查不到」里去猜。
>
> **四个本地化名(A2-1e 尾波加)**:同一条通知的**标题**也是从那个已发布面取的,所以本 op 若只出归属,它要救的那批未发布条目的通知就会**标题为空**——名与归属是同一个缺口的两半,补一半等于没补。
>
> **它仍然刻意不是 brief**:只有标题,**没有封面、没有简介、没有发售数据**。归属不是内容,一条回答「谁拥有这个条目」的道不能顺带变成读未发布正文的道。R2 红线亦不破——这是**幸存的 wiki 面**,wiki 自己的状态机本就住在这里,一个字节都不进 catalog 公开契约。

> **代码(单一真源)**:infra `apps/api/internal/galgameapp/`——`workflowroutes.go` = 15 工作流读的注册 + 逐路由章程;`devapiface.go` = `/internal` 面 devapi 链 + 2 feed 挂载;`writeroutes.go`(06a 写面)/ `proposeroutes.go`(06b 提案面)= 兄弟面。
>
> **机器可读 spec 注**:wiki 时代的机器可读 spec(`docs/galgame_wiki/read-openapi.yaml`、`calendar-openapi.yaml`,及门户发布的 `galgame-wiki.openapi.yaml` / `galgame-wiki-calendar.openapi.yaml`)已随桥面一同退役(W5)。平台工作流面**不再产出机器可读 spec**——与其兄弟 `/internal` 写面(06a)、提案面(06b)一致,二者本就无 spec;本面之真源即**代码(`workflowroutes.go`)+ 本页**。唯一对外的机器可读契约是门户发布的 `/v1` 公开投影 `docs/galgame_wiki/public-openapi.yaml`。

## `/api` staff 面 · taxonomy 读回(A2-1e 加 · 8 op)

`/api` 自 W3 起是 **staff-only** 面(admin / taxonomy CRUD·revert / catalog 浏览代理),此前**一条 GET 都没有**:读在 wave 05 迁去 `/internal`,又在 W5 退役到 `/v1`。于是两个管理台只能拿**列表行**去预填编辑表单——而 taxonomy 的 update 载荷是**整体替换**语义(字段给了就替换,不给才保留),**凡是读不回来的字段,保存时就被静默抹掉**。两站今天都在每次编辑时抹掉 `alias`(moyu 另抹 tag/official 的 `description`)。

本波按族补一对读回 op:

| op | 出参 |
|---|---|
| `GET /api/tag/search?q=` / `GET /api/tag/{id}` | 列表行 `{id,name}` / 记录 `{id,name,category,description,alias[]}` |
| `GET /api/official/search?q=` / `GET /api/official/{id}` | 列表行 `{id,name}` / 记录 `{id,name,original,link,lang,category,description,alias[]}` |
| `GET /api/engine/search?q=` / `GET /api/engine/{id}` | 列表行 `{id,name}` / 记录 `{id,name,description,alias[]}` |
| `GET /api/series/search?q=` / `GET /api/series/{id}` | 列表行 `{id,name}` / 记录 `{id,name,description,galgame_ids[]}` |

- **字段集 = 对应 `Update*Request` 的可编辑集,逐字段对齐**——读得回来的,正是写得进去的。这是本组 op 的全部意义,也是它的验收判据。
- **id 是 wiki id,端到端**(R11):与写 op、与修订历史同一键空间。公开浏览道迁向 catalog id(P2/R1)**刻意不伸进这条编辑道**——半迁两套 id 空间正是 R11 明令避免的事。
- **鉴权 = jwtAuth + `galgame.taxonomy.edit_any`**,与 update op **同一道门**:能读回编辑表单的人,按定义就是能写的人。无 JWT `401`,登录但无权 `403`。
- `search` 的 `q` 空白时:tag/official 返回空列表(它们是几千行的词表,无条件全量没有意义);engine/series 返回整个(小)facet ——两站的引擎/系列选择器本就是当扁平列表水化的。上限 50。
- 非法 id `400`,无此行 `404`。**无 spec**:`/api` 是 staff 面,与写面惯例一致不产出机器可读契约;真源 = 代码(`internal/platform/galgame/handler/staff_taxonomy_handler.go`)+ 本页。

## 终态(路线 B 已达成)

`/v1` 富化(05-open-api step 07 / W1a-d)已落地,下游 A 桶读全迁 `/v1` 同形公开契约(W2-W4),桥面公开数据读随之退役(W5)。平台展示的公开 API = 真实 `/v1`;`/internal` 仅余**平台工作流面**(留任,永久 S2S)。第三方实际开放(含 nsfw scope 发放)是独立的用户级决策,不在本轨。
