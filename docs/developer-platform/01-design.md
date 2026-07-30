# NextMoe 开放 API 与开发者平台 — 设计与架构

> 一句话定位:把 **NextMoe 开放 API**(生态作品数据的只读能力,按媒介/域分"面")安全、自助地开放给**第三方开发者**;配套 **NextMoe 开发者平台**(开发者门户)负责注册应用、领取凭证、查看用量、阅读文档。**不引入重型 API 网关**,而是复用我们已有的 OAuth2 IdP + Fiber + Redis + Cloudflare + Nuxt,把"开发者平台"那薄薄一层做进现有体系。

> 本设计按主题拆分为 01–07(见 [README](./README.md));**章节号 §1–§15 为跨文档稳定锚点**。本文承载:定位与命名约定、§1 背景与目标、§2 域名与部署拓扑、§3.3/§3.4 跨面互链与面挂载、§13 分期、§15 关键决策。其余章节:§3.1/§3.2/§3.5/§10 见 [02](./02-public-api.md)、§4/§7 见 [03](./03-auth-and-tiers.md)、§5/§6/§8/§12 见 [04](./04-platform-internals.md)、§9 见 [05](./05-developer-portal.md)、§11 见 [06](./06-security-compliance.md)、§14 见 [07](./07-migration-and-ops.md)。

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
> galgame 面(撰文时为独立 `cmd/galgame`/库 `kun_galgame_wiki`;wiki 退役 W1-W5 后由 `cmd/catalog` 承载、库 `kun_catalog`,契约不变。读面已 Huma 出谱)、
> catalog 服务(`cmd/catalog`,库 `kun_catalog`,生产在线,自带 Huma spec)、
> artifact 的 Huma/OpenAPI 样板(`cmd/gen-openapi`)、
> calendar 的 ETag/缓存样板(`internal/platform/galgame/handler/calendar_handler.go`)。

---

## 1. 背景与目标

### 1.1 现状(2026-07-11 修订)

- **两个真相源,各司其职**:
  - **galgame 内容真源 = wiki**(`cmd/galgame` / `kun_galgame_wiki`):多语名称/简介、封面/截图、tag、会社、发售、revision——所有 galgame 内容读操作走 wiki API(API-first),不向下游复制目录数据。**日落条款(2026-07-14,doc 19)**:wiki 按 W0-W5 波次整体退役,W2 后 galgame 内容真源 = catalog 侧内容体;`/v1/galgame/*` 契约与 gid 全程不变,后端换血对外零感知。**该公开面本身已于 2026-07-30(wave 146)提前摘牌,整族返回 `410 Gone`,后继面 = 正典 `/v1/catalog`。**
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
| NextMoe 开放 API(对外只读) | `api.nextmoe.dev` | Traefik 按路径分发:`/v1/catalog/*` 与 `/v1/galgame/*` 均 → catalog 服务(`cmd/catalog`)。**galgame 面已于 2026-07-30 摘牌**:其 Traefik 路由留任,只为把整族稳定地落到 `410 Gone` 而非 404 | `kun_catalog` |
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
- 与站点解耦,可施加**独立的缓存 / 限流 / CORS / WAF** 策略;公开只读响应在 **Cloudflare 边缘缓存**(见 [04 §8](./04-platform-internals.md)),把第三方流量挡在边缘,回源服务几乎不被打——这是"开放 API 代价可控"的关键。
- 鉴权在各面服务本地完成(JWKS 验签/introspection 缓存),**没有集中网关单点**——与生态"网关是触发式储备"的既定原则一致。

**为什么是 `nextmoe.dev` 族**:
- 对第三方 app,base URL 会被写死在代码里——今天用 `kungal.org` 系,将来迁 NextMoe 品牌就是一次对全部第三方的 Sunset 级破坏性变更;现在改只需要动本文档。
- `.dev` 语义贴合开发者面;文档门户已在 `nextmoe.dev` 族(`docs-kungal.nextmoe.dev`),品牌连续。
- `nextmoe.com` 按既定决策留给 NextMoe 本体揭幕,不提前消费。

---

## 3. 公开 API 面 — 跨面互链与面挂载

> 本节承载 §3 的架构面向。端点原则(§3.1)、v1 端点清单(§3.2)、稳定性承诺与演进条款(§3.5)见 [02-public-api.md](./02-public-api.md)。

### 3.3 跨面互链(「整合数据」的兑现方式)

**不搬数据,靠稳定 ID 互链**:

- catalog work 响应携带认领指针:`{"claimed_by": {"site": "galgame_wiki", "work_id": 1234}}` → 该指针**仍在**(它是身份事实,不是路由承诺),但 2026-07-30 起**不再有可调的公开 galgame 端点**(`/v1/galgame/1234` 落 410);内容详情走正典 `/v1/catalog/works/{id}`;
- galgame 详情响应携带 `catalog_work_id: "w56789"` → 开发者据此取 credits / 跨媒介关系 / 多源锚;
- 旧 ID 永久 301 到 canonical(redirect 语义写进 OpenAPI 描述,SDK 自动跟随)。

于是第三方拿到的是**几个站加在一起的整合视图**:wiki 的内容 + catalog 的人物/credits/关系/多源身份——而每份数据仍只有一个 owner。

### 3.4 面的挂载模型(未来)

- **新媒介内容面**:nextmanga / lolinovel / ani.today 上线时各挂 `/v1/manga/*` 等——新 Traefik 路由 + 该产品服务的公开投影,token/门户/SDK 不变。catalog 面从第一天就含全媒介注册行与关系图谱,所以"anime 改编自这部 galgame"这类边在 v1 就查得到,内容面后到。
- **统计面(Phase 2.5,前置已全清)**:跨源评分/发布时间分布/生态变迁。数据地基已在且**比 v2 设计时更好**:三源评分 meta 全在产(`galgame_vndb_meta` 62k / `galgame_bangumi_meta` 12.8k / `galgame_eg_meta` 15.6k,2026-07 三期落地)+ `galgame_stats` 6 键日更 + 站内读面(`GET /galgame/:gid/scores`、`GET /galgame/stats`)已上线——公开投影是薄封装。再分发姿态已拍板(D1,见 [06 §11](./06-security-compliance.md))。

---

## 13. 分期

| 阶段 | 内容 | 状态 |
|---|---|---|
| **Phase 1 地基** | 两面公开只读投影(`/v1/galgame/*` 白名单 + 游标分页 + **聚合记录 DTO(D1)** + **变更流**;`/v1/catalog/*` 白名单 + **lookup 外部 id 反查** + redirects 流)+ 公开 OpenAPI ×2 + Scalar 文档 + **API Key**(hash/show-once/轮换/吊销)+ Redis 限流 + **热路径缓存 + Cloudflare** + `api.nextmoe.dev` / `developer.nextmoe.dev` 域名 + **trusted tier 首批邀请 key** | 🚧 执行中(refs/plans/05-open-api) |
| **Phase 2** | 配额/分层打磨 + 用量面板 + 门户打磨 + **OAuth2 client_credentials** + scope 词表 + **MCP server(D4 提级:公开只读 API 同时暴露为 MCP,AI 助手/agent 直接查生态目录)** | ⬜ |
| **Phase 2.5 统计面** | 跨源评分/发布分布派生层(原拟挂 `/v1/galgame/stats/*`;该面 2026-07-30 摘牌后须改挂正典 `/v1/catalog` 之下);**前置已全清**(D1 拍板 + 三源评分 meta 在产,§3.4) | ⬜ 可随 Phase 2 实施 |
| **Phase 3** | `authorization_code`+PKCE **代表用户**(投稿/写)+ `galgame:nsfw` tier 闸 + 审批流 | ⬜ |
| **事件驱动(非阶段)** | manga / novel / anime 内容面随各产品上线挂载(§3.4);letmoe 相关的同人/asmr 注册行投影随其上线评估;**wiki 退役 W1-W5(doc 19)在 Phase 1 之后独立推进** | — |

> 依赖关系:Phase 1 里**缓存 + 公开 spec 是前置地基**(不是优化项)——没有缓存,"所有读走 API"会把回源服务打成瓶颈;没有公开 spec,门户文档/SDK 无从谈起。

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
