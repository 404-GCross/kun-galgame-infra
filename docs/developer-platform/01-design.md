# NextMoe 开放 API 与开发者平台 设计

> 一句话定位:把 **NextMoe 开放 API**(生态作品数据的只读能力,按媒介/域分"面")安全、自助地开放给**第三方开发者**;配套 **NextMoe 开发者平台**(开发者门户)负责注册应用、领取凭证、查看用量、阅读文档。**不引入重型 API 网关**,而是复用我们已有的 OAuth2 IdP + Fiber + Redis + Cloudflare + Nuxt,把"开发者平台"那薄薄一层做进现有体系。

> 状态:设计稿 v2.1(可实施)。v2.1(2026-07-14)按 `refs/docs/nextmoe-draft/19`(开放 API 计划与 galgame-wiki 退役)拍板落地:D1 公开投影=聚合记录、D2 tier 与 IdP 五角色集成、D4 MCP 提级 Phase 2、外部 id 反查(lookup)与变更流提入 Phase 1、wiki 真源加日落条款、VNDB 评分缺口已清(galgame_vndb_meta 在产)。战略上位与 W0-W5 退役波次见 doc 19。
> 状态历史:v2(2026-07-11)——v1(「鲲 Galgame 开发者平台」)按产品负责人方向修订:
> ① **品牌与域名升级为 NextMoe**(`nextmoe.dev` 族)——对第三方,base URL 是最贵的契约,品牌迁移必须发生在第一个外部消费者出现之前(暗建期品牌预埋,`refs/docs/nextmoe-draft/08` §5.1);
> ② 拓扑从"单一 galgame API"升级为"**统一平台 + 按面挂载**":v1 只亮 **galgame 面 + catalog 图谱面**,manga / novel / anime 面随各产品上线挂载,host / token / 门户 / 配额不变——加面不重做;
> ③ 吸收 catalog 生产化后的新事实(catalog 服务已在产、galgame 读面已 Huma 出谱),修正 v1 的过时前提。

> 命名约定(全文固定):
> - **NextMoe 开放 API** = 对外开放的只读 HTTP API 总称(`api.nextmoe.dev`),内部按媒介/域分**面**(face):**catalog 面**(跨媒介身份/图谱)、**galgame 面**(galgame 内容,v1 唯一的内容面)、(未来)manga / novel / anime 面。
> - **NextMoe 开发者平台** = 开发者门户 + 凭证/配额/用量管理(`developer.nextmoe.dev`)。
> - 开发者账号 = 鲲 Galgame 账户(IdP;后续随品牌升级更名「NextMoe 账户」——同一账号体系,改名不阻塞本设计)。

> 本设计建立在本仓既有能力之上:
> 自建 OAuth2 IdP(`oauth_clients` 表,`internal/platform/site/model/oauth_client.go`,主库 `kun_galgame_infra`)、
> galgame 服务(`cmd/galgame`,库 `kun_galgame_wiki`,读面已 Huma 出谱)、
> catalog 服务(`cmd/catalog`,库 `kun_catalog`,生产在线,自带 Huma spec)、
> artifact 的 Huma/OpenAPI 样板(`cmd/gen-openapi`)、
> calendar 的 ETag/缓存样板(`internal/platform/galgame/handler/calendar_handler.go`)。

---

## 1. 背景与目标

### 1.1 现状(2026-07-11 修订)

- **两个真相源,各司其职**:
  - **galgame 内容真源 = wiki**(`cmd/galgame` / `kun_galgame_wiki`):多语名称/简介、封面/截图、tag、会社、发售、revision——所有 galgame 内容读操作走 wiki API(API-first),不向下游复制目录数据。**日落条款(2026-07-14,doc 19)**:wiki 按 W0-W5 波次整体退役,W2 后 galgame 内容真源 = catalog 侧内容体;`/v1/galgame/*` 契约与 gid 全程不变,后端换血对外零感知。
  - **跨媒介身份/图谱真源 = catalog**(`cmd/catalog` / `kun_catalog`,生产在线):全媒介作品注册行(21 万+,含 anime/manga/novel/asmr)、人物名义/角色/厂牌实体族、credits(63 万级)、跨媒介关系、外部溯源锚(VNDB / Bangumi / DLsite / ErogameScape exact 锚)、redirect/resolve、三索引实体搜索。
- **契约基础比 v1 设计时更好**:galgame 读面(list / detail / batch / search / calendar / 官方·标签·引擎·系列 / revisions)已 Huma 出谱并入契约三门(spec→TS drift / code→spec / oasdiff);catalog 服务自带 `openapi.json`;calendar 已有 ETag/缓存样板。
- **缺口**:① 面向第三方的注册 / 凭证 / 配额 / 用量 / 门户;② 公开 `/v1` 投影(两个面的白名单子集,与内部 spec 解耦);③ 缓存铺开(calendar 之外的热路径);④ 源数据再分发的授权姿态(§15,拍板项)。

### 1.2 目标

1. 第三方开发者能**几分钟内**注册应用、拿到凭证、发出第一个成功请求。
2. 一个**精选、版本化、稳定**的公开只读 API 子集(绝不暴露 internal/admin/写)。
3. 自助门户:应用管理、凭证(show-once)、用量/配额、OpenAPI 交互文档。
4. 按 key/应用的**限流 + 配额 + 分层**,公开读**可被 Cloudflare 边缘缓存**。
5. **复用 IdP**:开发者账号 = 鲲 Galgame 账户(→ NextMoe 账户);应用 = `oauth_clients` 行;不另造认证系统、不上网关。
6. NSFW 默认关闭,按 scope / tier 显式闸控(ToS / 合规)。
7. **一次搭台,多面挂载**:token / 门户 / 配额 / 缓存策略是平台级的;未来每个新媒介站上线,开放 API 只是"加一个面",不重做平台。

### 1.3 非目标(v1)

- 不开放写 / admin 端点(投稿类写操作放 Phase 3,走 OAuth2 + 用户授权)。
- 不引入 Kong/Tyk/Gravitee 等网关(运维重、门户多在付费版;我们规模 + 已有件不划算)。Traefik 路径路由 + 各面服务本地鉴权中间件 = 去中心化的等价物。
- 不做付费 / 计费(仅做配额与分层的"业务上限",变现留待将来)。
- manga / novel / anime **内容面**不在 v1(产品尚未出生;catalog 面的全媒介注册层/关系图谱除外——它天然跨媒介,是 v1 的卖点之一)。
- MCP server 仅"留位"(见 §13 Phase 4)。

---

## 2. 域名与部署拓扑

三个域名,各自单独职责(均在 Cloudflare 后、Traefik 路由)。**开放 API 采用单一 host + 路径分面**:

| 角色 | 域名 | 后端 | 库 |
|---|---|---|---|
| NextMoe 开放 API(对外只读) | `api.nextmoe.dev` | Traefik 按路径分发:`/v1/catalog/*` → catalog 服务(`cmd/catalog`);`/v1/galgame/*` → galgame 服务(`cmd/galgame`;doc 19 W2 起改指 catalog 侧内容体,契约不变) | `kun_catalog` / `kun_galgame_wiki` |
| 开发者门户 | `developer.nextmoe.dev` | 门户前端(Nuxt)+ 平台后端(扩展 account/IdP 侧) | `kun_galgame_infra` |
| IdP(已存在) | 现有 oauth 域名 | `cmd/oauth` | `kun_galgame_infra` |

```
                         ┌────────────── Cloudflare(TLS + 边缘缓存) ───────────────┐
   第三方应用 ──────────▶ │  api.nextmoe.dev   developer.nextmoe.dev   <oauth 域名>  │
   (服务端持 API Key       └──────┬────────────────────┬────────────────────┬───────┘
    或 OAuth2 token)              │                    │                    │
                              Traefik(按路径分面)   Traefik              Traefik
                        ┌─────────┴─────────┐          │                    │
                 /v1/catalog/*        /v1/galgame/*    │                    │
                 catalog 服务          galgame 服务   门户前端+平台后端    IdP(cmd/oauth)
                 kun_catalog        kun_galgame_wiki  kun_galgame_infra   kun_galgame_infra
                        │                  │            └── oauth_clients / developer_api_keys / usage
                        └──── 每面同一套中间件:鉴权 → 限流 → 配额 → scope → content_limit
                              (API key 经 IdP introspection,Redis 缓存;JWT 本地验签)
```

**为什么单一 API host(`api.nextmoe.dev`)+ 路径分面**:
- 对第三方,**一个 base URL + 一份凭证**覆盖全生态数据——这是"NextMoe 开放 API"而非"N 个站各自的 API"的产品含义。
- 未来挂载 manga/novel/anime 面 = 加一条 Traefik 路由 + 一个面服务,**host / token / 门户 / SDK 基座全部不变**。
- 与站点解耦,可施加**独立的缓存 / 限流 / CORS / WAF** 策略;公开只读响应在 **Cloudflare 边缘缓存**(见 §8),把第三方流量挡在边缘,回源服务几乎不被打——这是"开放 API 代价可控"的关键。
- 鉴权在各面服务本地完成(JWKS 验签/introspection 缓存),**没有集中网关单点**——与生态"网关是触发式储备"的既定原则一致。

**为什么是 `nextmoe.dev` 族**:
- 对第三方 app,base URL 会被写死在代码里——今天用 `kungal.org` 系,将来迁 NextMoe 品牌就是一次对全部第三方的 Sunset 级破坏性变更;现在改只需要动本文档。
- `.dev` 语义贴合开发者面;文档门户已在 `nextmoe.dev` 族(`docs-kungal.nextmoe.dev`),品牌连续。
- `nextmoe.com` 按既定决策留给 NextMoe 本体揭幕,不提前消费。

---

## 3. 公开 API 面与端点(精选 + 版本化)

### 3.1 原则

- **白名单暴露**:只把精选的只读端点放进 `api.nextmoe.dev/v1/…`;internal / admin / 写端点**永不**进入公开路由(物理上不挂到公开路由组)。
- **URL 版本化** `/v1/`:一旦有了无法协调破坏性变更的外部开发者,版本化与弃用策略从"过早优化"变成"硬需求"。
- **弃用策略**:破坏性变更必须升 `/v2/`;字段级弃用走 `Deprecation` / `Sunset` 响应头 + 门户公告 + 不少于 N 个月窗口。
- **路径命名空间 = 面**:`/v1/catalog/*`、`/v1/galgame/*`,未来 `/v1/manga/*` 等。galgame 的领域词表(officials/tags/engines/series)全部收进 `/v1/galgame/` 之下,给未来媒介留干净的顶层命名空间。
- **公开投影与内部契约解耦**:公开面是从既有 Huma spec 精选出的**独立 spec**;内部 S2S/站点契约继续自由演进,互不牵制。

### 3.2 v1 端点清单(草案)

**galgame 面**(后端 = `cmd/galgame`,内容真源):

| 公开端点(`/v1`) | 映射内部 | scope | 说明 |
|---|---|---|---|
| `GET /v1/galgame` | `GET /galgame`(List) | `galgame:read` | 分页/排序/搜索/发售范围;**游标分页**(见 §8 备注) |
| `GET /v1/galgame/{id}` | `GET /galgame/:gid` | `galgame:read` | 详情;响应携带 `catalog_work_id`(跨面互链,见 3.3) |
| `GET /v1/galgame/batch` | `GET /galgame/batch` | `galgame:read` | 批量(brief/detail) |
| `GET /v1/galgame/search` | `GET /galgame/search` | `galgame:read` | Meilisearch |
| `GET /v1/galgame/calendar*` | calendar 三件套 | `galgame:read` | 已有 ETag/缓存,直接复用 |
| `GET /v1/galgame/officials` `…/{id}` `…/{id}/galgames` | official List/Get/members | `galgame:read` | 会社目录 + 成员 |
| `GET /v1/galgame/tags` `…/{id}` `…/{id}/galgames` | tag | `galgame:read` | |
| `GET /v1/galgame/engines` / `GET /v1/galgame/series` … | engine/series | `galgame:read` | |
| `GET /v1/galgame/changes` | (新增,updated 时间戳 keyset) | `galgame:read` | **变更流**(doc 19 D5,Phase 1):增量同步游标,管理器免全量重爬 |
| (Phase 3)`POST /v1/galgame/{id}/submit` 等 | 投稿/PR | `galgame:submit` | 需 OAuth2 用户授权 |

**catalog 面**(后端 = `cmd/catalog`,跨媒介身份/图谱真源):

| 公开端点(`/v1`) | scope | 说明 |
|---|---|---|
| `GET /v1/catalog/works/{id}` | `catalog:read` | 注册行:display_name / titles / medium / 分级 / 外部锚(来源白名单过滤,§11)/ **认领指针**(→ 内容面路由,见 3.3) |
| `GET /v1/catalog/works/{id}/credits` | `catalog:read` | 该作品的 credits(名义/角色/role) |
| `GET /v1/catalog/works/{id}/relations` | `catalog:read` | 跨媒介关系(改编/续作/同世界观…,单行双向渲染) |
| `GET /v1/catalog/names/{id}`(+ `…/credits`) | `catalog:read` | 名义(credited identity;{id}=credit_name id,携 person_id+公开 sibling 名义)——**hidden 名义链接不出现在公开聚合**(既有可见性政策)。v2.1 实施时由 persons/{id} 更名:实体层 credits 指向名义而非 person,公开词表与 resolve/redirects 的 "name" 键统一 |
| `GET /v1/catalog/characters/{id}` | `catalog:read` | 角色(含出演,spoiler 级字段) |
| `GET /v1/catalog/labels/{id}`(+ `…/works`) | `catalog:read` | 厂牌/文库/社团 |
| `GET /v1/catalog/search` | `catalog:read` | 实体搜索(persons/characters/labels,复用三索引) |
| `POST /v1/catalog/resolve` | `catalog:read` | 批量旧 ID → canonical(redirect 压平语义与内部一致) |
| `GET /v1/catalog/lookup` + `POST …/lookup/batch` | `catalog:read` | **外部 id 反查(killer,doc 19 §3.1,Phase 1)**:`?source=vndb&external_id=v19658` → work + `claimed_by` 指针;批量 ≤100。背书 = 四源 exact 锚(在产) |
| `GET /v1/catalog/redirects` | `catalog:read` | id 收敛事件 keyset 流(内部 S2S 面公开化,doc 19 §3.3) |

> 不进入公开路由:`/admin/*`、人审队列、merge/claim 等 S2S 写面、`/:gid/revert`、消息队列、site 管理等。
> catalog 面范围备注:`stub`(无锚且元数据不达标的未认领行)不进公开聚合——既有不变量,公开面直接继承;asmr/同人未认领波是否进 v1 投影,并入 §15 再分发授权一起拍板(倾向:v1 先只放 galgame 可达闭包 + 跨媒介关系可达行,letmoe 上线时再扩)。

### 3.3 跨面互链(「整合数据」的兑现方式)

**不搬数据,靠稳定 ID 互链**:

- catalog work 响应携带认领指针:`{"claimed_by": {"site": "galgame_wiki", "work_id": 1234}}` → 开发者据此调 `/v1/galgame/1234` 取内容详情;
- galgame 详情响应携带 `catalog_work_id: "w56789"` → 开发者据此取 credits / 跨媒介关系 / 多源锚;
- 旧 ID 永久 301 到 canonical(redirect 语义写进 OpenAPI 描述,SDK 自动跟随)。

于是第三方拿到的是**几个站加在一起的整合视图**:wiki 的内容 + catalog 的人物/credits/关系/多源身份——而每份数据仍只有一个 owner。

### 3.4 面的挂载模型(未来)

- **新媒介内容面**:nextmanga / lolinovel / ani.today 上线时各挂 `/v1/manga/*` 等——新 Traefik 路由 + 该产品服务的公开投影,token/门户/SDK 不变。catalog 面从第一天就含全媒介注册行与关系图谱,所以"anime 改编自这部 galgame"这类边在 v1 就查得到,内容面后到。
- **统计面(Phase 2.5,前置已全清)**:跨源评分/发布时间分布/生态变迁。数据地基已在且**比 v2 设计时更好**:三源评分 meta 全在产(`galgame_vndb_meta` 62k / `galgame_bangumi_meta` 12.8k / `galgame_eg_meta` 15.6k,2026-07 三期落地)+ `galgame_stats` 6 键日更 + 站内读面(`GET /galgame/:gid/scores`、`GET /galgame/stats`)已上线——公开投影是薄封装。再分发姿态已拍板(D1,§11)。

### 3.5 稳定性承诺

- 已发布字段不删不改语义;只做**向后兼容**的新增。
- 公开 `content_limit` 语义统一(见 §11);各端点默认 = `sfw`。
- catalog 面的实体 ID 全局稳定,合并只产生 redirect,永不复用。~~`w`/`p`/`n`/`b`/`c` 前缀~~(superseded,2026-07-15 步骤 03 裁定 2:公开 id = 纯数字——与 galgame 面已冻结的 `catalog_work_id` 数字形态一致,路径已按实体类型分命名空间)。公开线源键 = 站点真拼写(`erogamescape`;内部注册表键 `erogamespace` 在投影层映射,lookup 双拼容错)。

**演进条款**(step 07 落账,Phase 2「查询灵活性」引入时形式化;五条共同定义"什么样的改动是加性、什么样必须升版本"):

1. **加法优先,永不改语义**:已发布字段的名称、类型、含义与 null 语义一律冻结;演进只能是**新增**可选字段 / 可选查询参数 / 新端点。任何"改"都不是加性,一律走破坏性变更流程(第 3 条)。新增可选参数(如 `include` / `fields`)与新增可选响应键,对既有客户端逐字节无影响——缺省响应恒等于冻结契约。
2. **客户端「必须忽略未知字段」= 契约条款**(升格):公开响应可能在任何时候新增字段;**合规客户端必须容忍并忽略它不认识的字段**。对称地,服务端对 `fields=` / `include=` 里的未知名**静默忽略、绝不 400**(双向前后兼容:老客户端遇新字段不炸,新客户端拼错字段名不炸)。这条对侧承诺正是"加法优先"能成立的前提——加字段对所有正确实现的消费者都无破坏。
3. **破坏性变更 = `/v2` 并行**:确需改语义 / 删字段 / 改类型时,新增 `/v2` 与 `/v1` **并行运行**,旧版打 `Deprecation` / `Sunset` 响应头 + 门户 changelog 公告 + **不少于 12 个月**的迁移窗口;窗口内 `/v1` 不下线、语义不动。
4. **内部面 = 公开契约的试验缓冲层**:新字段 / 新形状先在内部 S2S / 站点读面消化验证,形状稳定后再投影到公开面冻结。公开面永远是内部契约的**精选滞后投影**,不承载未经内部实战的实验形状——这样绝大多数迭代压力被内部面吸收,公开契约的破坏性变更趋近于零。
5. **新数据源 = 加键,新媒介 = 加面**:新增第四 / 第五源评分或外部锚,是在 `refs` / `scores` 等**键控对象**上加键(这正是把它们设计成键控对象而非并列标量字段的本意);新增媒介(manga / novel…)是加新面 `/v1/<medium>/*`。两者都是加性演进,天然不触碰既有契约。

---

## 4. 认证与授权

两条腿,按风险分:

### 4.1 API Key —— 主入口,面向"读公开目录"

- **格式**:`nm_live_<base62(24B)>` / `nm_test_<base62(24B)>`(`nm` = NextMoe;前缀区分环境;前缀便于密钥泄漏扫描器识别)。
- **存储**(复用 `oauth_client.go` 的 `HashOAuthClientSecret` 模式):
  - 库里**只存 `sha256(key)` 的 hex**,带 `sha256:` 前缀;**明文仅创建时显示一次**,永不落库。
  - 另存 `key_prefix`(如 `nm_live_a1b2`)与 `last4` 供门户识别。
  - 校验用 `crypto/subtle` 常量时间比较(同 `VerifySecret`)。
- **传递**:`Authorization: Bearer nm_live_…`(统一用 Authorization;`X-API-Key` 作兼容备选)。
- **一个应用可有多把 key**:支持**轮换**(签发新 key,旧 key 设未来 `expires_at`,宽限 24–72h,不瞬杀)与**吊销**(`revoked_at`,下次请求即拒)。
- **默认 scope** = `catalog:read` + `galgame:read`(只读公开);NSFW 需单独 scope + 更高 tier。
- API key 是**机密**:只能服务端使用;浏览器直连第三方用 OAuth2 public client + PKCE,**不发 key**。
- **一把 key 走遍所有面**:限流/配额计数是平台级(跨面合并计数),per-面权限用 scope 表达。

### 4.2 OAuth2 —— 写 / 代表用户的操作,扩展 IdP

- **应用 = `oauth_clients` 行**(第三方:`AutoConsent=false` → 必看同意页;SPA 用 `IsPublic=true` + PKCE)。
- 复用现有 grant 白名单(`Grants`)+ scope 白名单(`AllowedScopes` / `CheckScope`):
  - `client_credentials`:应用自身(app-only)读受限资源。
  - `authorization_code` + PKCE:**代表某用户**(如代为投稿)。
- **scope 词表**(按面命名,起步最小,可扩展):
  - `catalog:read` `galgame:read`(公开读;未来 `manga:read` 等同构生长)
  - `galgame:nsfw`(放开 NSFW,需 tier 批准)
  - `galgame:submit` `user:read`(Phase 3)

### 4.3 校验路径(各面服务侧)

面服务**不直接读** `kun_galgame_infra`;凭证在各面边缘解析,**两个面共用同一套中间件实现**(落地为共享中间件包——`kungal-kit` 候选;或过渡期同构复制,收敛时机随 kit):

- **Bearer JWT**(OAuth2):本地验签(资源服务对一方 token 已这么做)→ 取 `client_id` + `scope`。
- **API Key**:调 IdP 的内部 introspection 端点 → **Redis 缓存**结果(短 TTL,如 60s),避免每请求打 IdP。

introspection 契约(IdP 新增,内部 s2s):
```
POST /oauth/apikey/introspect          (s2s, 仅内网/带 s2s 凭证)
  { "key": "nm_live_…" }
→ 200 { "active": true, "client_id": "...", "app_name": "...",
        "scopes": ["catalog:read", "galgame:read"], "tier": "free",
        "nsfw_allowed": false, "key_id": 123, "rate_per_min": 60, "quota_daily": 50000 }
→ 200 { "active": false }   // 未知/已吊销/已过期
```
> 备选:若与 image/artifact 现有的 client 校验机制(site-key)一致地"共享 DB 读",可改为面服务直读 `oauth_clients`/`developer_api_keys`——**实现时与现有 image/artifact 的 client 校验路径对齐**,二选一,保持一致。

---

## 5. 数据模型(均在主库 `kun_galgame_infra`)

### 5.1 扩展 `oauth_clients`(沿用 Image*/Artifact* 扩展字段范式)

应用(无论 API-key-only 还是 OAuth2)都是一行 `oauth_clients`,新增。**字段是平台级**(tier/配额对整个开放 API 生效,不按媒介拆——per-面权限走 scope):

```go
// --- Developer platform / NextMoe open API extension fields ---
// OwnerUserID: 第三方开发者应用的拥有者(生态账号)。一方站点 client 为 NULL
// (它们靠 SiteID 归属)。门户的 "我的应用" 按此过滤,也用于管理鉴权。
OwnerUserID    *uint  `gorm:"index" json:"owner_user_id,omitempty"`

DevEnabled     bool   `gorm:"not null;default:false" json:"dev_enabled"`      // 准入 NextMoe 开放 API
DevTier        string `gorm:"size:20;not null;default:'free'" json:"dev_tier"` // free|trusted|internal(D2:tier 授予由平台内部完成;身份/角色沿 IdP 五全局角色,不铸新全局角色)
DevNSFWAllowed bool   `gorm:"not null;default:false" json:"dev_nsfw_allowed"`
// 限流/配额(0 = 用 tier 默认值,见 §7)
DevRatePerMin  int    `gorm:"not null;default:0" json:"dev_rate_per_min"`
DevQuotaDaily  int    `gorm:"not null;default:0" json:"dev_quota_daily"`
```
> scope 直接复用既有 `AllowedScopes` + `CheckScope`,不另起字段。

### 5.2 新表 `developer_api_keys`

```go
type DeveloperAPIKey struct {
    ID          uint       `gorm:"primaryKey" json:"id"`
    ClientID    string     `gorm:"size:50;not null;index" json:"client_id"` // FK oauth_clients.id(= 应用)
    Name        string     `gorm:"size:100;not null" json:"name"`           // 开发者起的标签
    KeyHash     string     `gorm:"size:80;not null;uniqueIndex" json:"-"`   // "sha256:<hex>"
    KeyPrefix   string     `gorm:"size:24;not null;index" json:"key_prefix"`// nm_live_a1b2
    Last4       string     `gorm:"size:4;not null" json:"last4"`
    Scopes      datatypes.JSON `gorm:"type:jsonb" json:"scopes"`            // ⊆ 应用 AllowedScopes
    NSFWAllowed bool       `gorm:"not null;default:false" json:"nsfw_allowed"`
    ExpiresAt   *time.Time `json:"expires_at,omitempty"`  // 轮换宽限/有效期;NULL=不过期
    RevokedAt   *time.Time `json:"revoked_at,omitempty"`  // 吊销即拒
    LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
    CreatedByUserID uint   `gorm:"not null" json:"created_by_user_id"`
    CreatedAt   time.Time  `json:"created_at"`
}
func (DeveloperAPIKey) TableName() string { return "developer_api_keys" }
```
有效性:`active = RevokedAt IS NULL AND (ExpiresAt IS NULL OR ExpiresAt > now())`。

### 5.3 新表 `developer_api_usage`(用量落盘,按日聚合)

```go
type DeveloperAPIUsage struct {
    ID        uint      `gorm:"primaryKey"`
    ClientID  string    `gorm:"size:50;not null;uniqueIndex:idx_usage_day,priority:1"`
    KeyID     *uint     `gorm:"uniqueIndex:idx_usage_day,priority:2"` // NULL=应用级汇总
    Day       string    `gorm:"size:10;not null;uniqueIndex:idx_usage_day,priority:3"` // YYYY-MM-DD(JST)
    Count     int64     `gorm:"not null;default:0"`
    Status4xx int64     `gorm:"not null;default:0"`
    Status5xx int64     `gorm:"not null;default:0"`
    UpdatedAt time.Time
}
```
> 实时计数在 **Redis**(限流/配额计数器),周期(如每分钟)flush 到此表供门户出历史图;`last_used_at` 同理异步回写。
> 用量维度可选加 `face` 列(catalog/galgame)——成本极低,门户能出"按面"曲线;v1 先按 (client, key, day),见 §15。

> **迁移**:以上列 + 表都在 `kun_galgame_infra` → `go run ./cmd/migrate`(部署不自动跑,见 §14)。

---

## 6. 请求生命周期(API 中间件链)

```
Cloudflare(TLS,可能命中边缘缓存→直接返回)
  → Traefik(按 /v1/<face>/ 路由到面服务)
    → 面服务(catalog 或 galgame,同一套中间件):
       1. resolveCredential:  Bearer key/JWT → app + scopes + tier + nsfw_allowed
                              (JWT 本地验;API key 经 introspection + Redis 缓存)
                              失败 → 401
       2. rateLimit(Redis,滑动窗口,key=app/key,跨面共享计数) → 超 → 429 + Retry-After + X-RateLimit-*
       3. quota(Redis 当日计数器,跨面共享) → 超 → 429 + X-Quota-*
       4. requireScope(端点所需 scope ⊆ 凭证 scope) → 缺 → 403
       5. content_limit 闸:默认 sfw;请求 nsfw 需 galgame:nsfw scope + nsfw_allowed,否则降级为 sfw 或 403
       6. handler(命中 ETag → 304;否则查询 + 设缓存头)
       7. async:Redis incr 用量 + last_used_at;(周期 flush 落库)
```

伪代码(中间件):
```go
func OpenAPIAuth(c fiber.Ctx) error {
    cred, err := resolveCredential(c)            // API key(introspect+cache) 或 JWT
    if err != nil { return resp401(c) }
    if !allowRate(cred) { return resp429(c, retryAfter) }   // Redis 滑窗
    if !allowQuota(cred) { return resp429Quota(c) }         // Redis 日计数
    c.Locals("cred", cred)
    return c.Next()
}
// handler 内:requireScope("galgame:read"); content_limit = gate(c.Query("content_limit"), cred)
```

---

## 7. 限流 + 配额 + 分层

**两件不同的事**:限流 = 短期防滥用(req/min);配额 = 业务上限(req/day)。**计数是平台级**(一把 key 在所有面共享同一份额度)。

| tier | rate/min | quota/day | NSFW | 适用 |
|---|---|---|---|---|
| `free` | 60 | 50,000 | 否 | 默认,自助注册即得 |
| `trusted` | 600 | 1,000,000 | 可申请 | 邀请/审批的合作开发者(doc 19 D2:首批 = 友好 galgame 管理器项目) |
| `internal` | 不限 | 不限 | 是 | 一方应用(forum/moyu/letmoe;doc 19 W3 起 kungal/moyu 以此 tier 真实消费) |

> **tier 治理(D2 拍板)**:开发者身份与角色 = IdP 五全局角色(`docs/integration/oauth/11-roles.md`,冻结,不新增);tier / scope / NSFW / 配额等**细粒度授权 = 开发者平台内部数据**(`oauth_clients.dev_*` + key 行),由平台管理面授予——与 permission-first 教义同构(角色只是权限捆的入口,代码只查权限)。

- Redis 实现:限流用滑动窗口(`ratelimit:{key}:{minute}`),配额用当日计数(`quota:{key}:{YYYY-MM-DD}`,TTL 到次日)。
- 响应头:`X-RateLimit-Limit/Remaining/Reset`、`Retry-After`、`X-Quota-Limit/Remaining`。门户实时显示剩余配额。
- 应用/key 上的 `DevRatePerMin`/`DevQuotaDaily` 为 0 时用 tier 默认值(同 `RefreshTokenTTL()` 的"0=用默认"范式)。

---

## 8. 缓存(公开读的承重墙)

**关键设计:把"鉴权"与"响应内容"解耦,让公开读对 Cloudflare 可缓存。**

- 同一 `content_limit` + 同一版本下,公开目录读的**响应内容对所有调用者相同**(与是哪把 key 无关)。鉴权只用于**限流/计量**,不改变响应体。
- 因此缓存键 = `(path, query, content_limit, /v1)`,**不含 key**;响应可带 `Cache-Control: public, s-maxage=…`,被 Cloudflare 边缘共享缓存。
- 把 calendar 已验证的模式铺到两个面的热路径(galgame list/detail/batch/官方成员;catalog works/persons/labels 详情):
  - 弱 **ETag**(嵌 `max(updated)` 或资源指纹)→ `If-None-Match` 命中回 304。
  - `Cache-Control`:历史/稳定数据 `s-maxage` 长(如 1 天),易变的短(如 5 分钟);`max-age=0` 让浏览器每次回源校验。
  - `Cache-Tag`(Cloudflare Cache Rules / 按内容键)便于精准失效。
- 鉴权失败 / 配额头等**不可缓存**部分,仍在回源层处理(CF 仅缓存 2xx 公开读)。
- catalog 面的 **301 redirect 响应同样可缓存**(旧 ID → canonical 是永久事实)。
- **备注(吸收自 API 设计 skill,用在对的地方)**:`GET /v1/galgame` 列表在 6 万→10 万+ 目录上,offset 深翻页有性能悬崖 → 公开列表改 **游标分页**(`cursor`/`next_cursor`),既稳又对缓存友好。

> 这一节是"开放 API 代价可控"的核心:做好缓存 + CF,绝大多数公开读在边缘命中,回源服务不被打爆。

---

## 9. 开发者门户(`developer.nextmoe.dev`)

- **账号复用**:用生态账号经 IdP 登录即开发者账号,**不另造身份**(品牌显示随「NextMoe 账户」改名同步,机制零变)。
- **核心功能**:
  1. 创建应用(= 一行 `oauth_clients`,`owner_user_id=当前用户`,`dev_enabled=true`)→ 拿 `client_id`(OAuth)。
  2. 管理 **API Keys**:创建(**show-once** 明文)、看 `prefix+last4+last_used`、轮换(带宽限)、吊销。
  3. **用量/配额**:实时剩余 + 历史曲线(读 `developer_api_usage`)。
  4. **OpenAPI 文档**:用 **Scalar** 渲染(MIT、Try-It 最强、支持 OAuth flow、可嵌 Nuxt);两份公开 spec(catalog 面 / galgame 面)分 tab 呈现,未来媒介面同构加 tab。
  5. 申请更高 tier / NSFW(走审批)。
- **技术**:门户前端 Nuxt(`apps/` 下新增或并入现有);平台后端扩展 account/IdP 侧的 API(应用/key/用量 CRUD,鉴权用现有 JWT + `owner_user_id` 归属校验)。

---

## 10. OpenAPI 策略

v1 设计时"galgame 无 spec"的前提已过时——现状是**两个面都有 code-first spec**,工作量收敛为"公开投影":

- **galgame 面**:读面已 Huma 出谱(条件缓存端点为 spec-only 形态)。公开 `/v1` 投影 = 沿同一管线(`cmd/gen-openapi` 加一个 public 目标)产出**独立的公开 spec**(白名单端点 + `/v1` 前缀 + 公开 DTO),与内部 spec 解耦。
- **catalog 面**:服务自带 Huma spec(`/openapi.json`)。同法产出公开投影(白名单只读子集)。
- 产出 `api.nextmoe.dev/v1/catalog/openapi.json` 与 `…/v1/galgame/openapi.json` → 门户 Scalar 渲染 → 第三方据此生成 SDK(TS 优先,`@kungal/api-*` 发包纪律届时启用)。
- 公开 spec 纳入 `docs:verify` + oasdiff 破坏性门,升级为 **Tier-A 对外契约**(在 kungal-docs 登记)。

---

## 11. 安全 / 滥用 / 合规

- **HTTPS 强制**(Cloudflare);key 只走 header,**不进 URL、不进日志**(日志只留 `key_prefix`)。
- **NSFW**:默认 `sfw`;放开需 `galgame:nsfw` scope + `nsfw_allowed` tier,并审计——NSFW 闸控是**合规问题**(ToS / 法律),不只是整洁问题。catalog 面同理:`content_rating=r18` 的作品行默认过滤,同一 scope 闸控。
- **来源投影(再分发授权,D1 已拍板 2026-07-14)**:公开投影 = **聚合记录**——一个 Galgame 的每个字段是多源归并的结果(名称可能来自 wiki 策展、简介来自 Bangumi、日期来自 VNDB),**不做任何逐源原始字段的批量再分发**;评分以逐源数值 + 归源链接形态出现(P-★ 窄片同款),响应携带 `attribution` 块。归并结果与自产字段(中文简介/tag 本地化/竖图/stats)是投影本体;per-field provenance 机制用于执行该姿态。
- **CORS**:`api.nextmoe.dev` 对浏览器直连**不开放任意 origin 携带 API key**(key 是机密,仅服务端);浏览器场景走 OAuth2 public client + PKCE。
- **ToS / 滥用**:服务条款 + 异常用量告警 + 一键吊销 key/应用。
- **审计**:key 创建/轮换/吊销、tier 变更、异常 4xx/5xx 速率,写审计日志。

---

## 12. 可观测 / 计量

- Redis 实时计数(限流/配额)→ 周期 flush `developer_api_usage` → 门户曲线 + 配额执行 + 告警。
- 每请求:`last_used_at` 异步回写;按 (client, key, day) 聚合 count/4xx/5xx(可选加 face 维度,§15)。

---

## 13. 分期

| 阶段 | 内容 | 状态 |
|---|---|---|
| **Phase 1 地基** | 两面公开只读投影(`/v1/galgame/*` 白名单 + 游标分页 + **聚合记录 DTO(D1)** + **变更流**;`/v1/catalog/*` 白名单 + **lookup 外部 id 反查** + redirects 流)+ 公开 OpenAPI ×2 + Scalar 文档 + **API Key**(hash/show-once/轮换/吊销)+ Redis 限流 + **热路径缓存 + Cloudflare** + `api.nextmoe.dev` / `developer.nextmoe.dev` 域名 + **trusted tier 首批邀请 key** | 🚧 执行中(refs/plans/05-open-api) |
| **Phase 2** | 配额/分层打磨 + 用量面板 + 门户打磨 + **OAuth2 client_credentials** + scope 词表 + **MCP server(D4 提级:公开只读 API 同时暴露为 MCP,AI 助手/agent 直接查生态目录)** | ⬜ |
| **Phase 2.5 统计面** | 跨源评分/发布分布派生层(→ `/v1/galgame/stats/*`);**前置已全清**(D1 拍板 + 三源评分 meta 在产,§3.4) | ⬜ 可随 Phase 2 实施 |
| **Phase 3** | `authorization_code`+PKCE **代表用户**(投稿/写)+ `galgame:nsfw` tier 闸 + 审批流 | ⬜ |
| **事件驱动(非阶段)** | manga / novel / anime 内容面随各产品上线挂载(§3.4);letmoe 相关的同人/asmr 注册行投影随其上线评估;**wiki 退役 W1-W5(doc 19)在 Phase 1 之后独立推进** | — |

> 依赖关系:Phase 1 里**缓存 + 公开 spec 是前置地基**(不是优化项)——没有缓存,"所有读走 API"会把回源服务打成瓶颈;没有公开 spec,门户文档/SDK 无从谈起。

---

## 14. 迁移与运维提醒

- **数据库迁移(主库 `kun_galgame_infra`)**:新增 `oauth_clients` 列(`owner_user_id` + `dev_*`)+ 新表 `developer_api_keys` / `developer_api_usage` → **`go run ./cmd/migrate`**。⚠️ 部署**不自动**跑迁移,漏跑 = GORM 读不存在的列 → 静默失败(参见仓库历史教训)。
- **新域名**:`api.nextmoe.dev`、`developer.nextmoe.dev` → DNS + Cloudflare(含公开读的 Cache Rules)+ Traefik 路由(按 `/v1/<face>/` 分发到 catalog/galgame 服务)+ 各后端 CORS allowlist。
- **Redis**:新增 `ratelimit:*` / `quota:*` / `apikey:*`(introspection 缓存)键空间。
- **契约**:公开 spec ×2 纳入 `docs:verify` + oasdiff,在 kungal-docs 登记为对外 Tier-A 契约。
- **面服务中间件**:catalog/galgame 两面共用鉴权/限流/配额中间件——首选提取为共享包(`kungal-kit` 候选);过渡期同构复制时,两处必须同步演进(写进各自 README owner 声明)。

---

## 15. 关键决策 / 待定

1. ~~API 域名定名~~ **已定(2026-07-11)**:`nextmoe.dev` 族(`api.` / `developer.`);`nextmoe.com` 留给本体揭幕。
2. **API key 校验机制**:IdP introspection(解耦)还是面服务直读 DB(与 image/artifact 的 site-key 校验对齐)——按现有机制取齐。
3. **OpenAPI 公开投影落地**:`cmd/gen-openapi` 加 public 目标(推荐,单管线)vs 独立 authored spec + verify。
4. **公开列表分页**:offset → 游标(推荐,配合缓存与大目录)。
5. **用量计量粒度**:仅按 (key, day),还是加 face/端点维度(成本 vs 洞察)。
6. **NSFW 开放策略**:是否对外开放、以何 tier/审批门槛(合规决定;catalog 面 r18 行同一策略)。
7. ~~★ 源数据再分发授权~~ **已拍板(2026-07-14,doc 19 D1)**:公开投影 = 聚合记录(逐字段多源归并 + attribution 块),不做逐源原始字段批量再分发;评分 = 逐源数值 + 归源链接;asmr/同人未认领行仍倾向 v1 先只放 galgame 可达闭包(letmoe 上线时扩)。
8. ~~VNDB 评分摄入~~ **已清(2026-07,三期)**:`galgame_vndb_meta` 62k 行在产 + 05:15 日更;统计面数据前置全清。
9. ~~一方站点是否迁移到开放 API 面~~ **方向已定(2026-07-14,doc 19)**:kungal/moyu 终态**直接消费开放 API**(internal tier,W3 切换),wiki 整体退役;开放 API 的公开配额/缓存策略与 internal tier 隔离(internal 走内网 base,不占公开配额)。
