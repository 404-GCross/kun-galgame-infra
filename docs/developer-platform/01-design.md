# 鲲 Galgame 开发者平台 设计

> 一句话定位:把 **鲲 Galgame API**(galgame wiki 的只读能力)安全、自助地开放给**第三方开发者**;配套一个独立域名的 **鲲 Galgame 开发者平台**(开发者门户)负责注册应用、领取凭证、查看用量、阅读文档。**不引入重型 API 网关**,而是复用我们已有的 OAuth2 IdP + Fiber + Redis + Cloudflare + Nuxt,把"开发者平台"那薄薄一层做进现有体系。

> 状态:设计稿(待评审)。建立在本仓既有能力之上:
> 自建 OAuth2 IdP(`oauth_clients` 表,`internal/platform/site/model/oauth_client.go`,主库 `kun_galgame_infra`)、
> galgame 服务(`cmd/galgame`,库 `kun_galgame_wiki`)、
> artifact 的 Huma/OpenAPI 样板(`cmd/gen-openapi`)、
> calendar 的 ETag/缓存样板(`internal/platform/galgame/handler/calendar_handler.go`)。

> 命名约定(全文固定):
> - **鲲 Galgame API** = 对外开放的 galgame 只读 HTTP API(本设计的产品)。
> - **鲲 Galgame 开发者平台** = 开发者门户 + 凭证/配额/用量管理(本设计的承载体)。

---

## 1. 背景与目标

### 1.1 现状(本轮讨论已确立)

- **wiki 为 galgame 信息的唯一真源**;所有 galgame 读操作走 wiki API(API-first)。不向下游复制目录数据。
- galgame API **面广且成熟**(list / detail / batch / search(Meilisearch)/ calendar / 官方·标签·引擎·系列 / revisions / 投稿等),`content_limit` 基本统一,契约由 `docs/integration/galgame_wiki/` + `docs:verify` 守护。
- **缺口**:① 没有面向第三方的注册 / 凭证 / 配额 / 用量 / 门户;② galgame 还没有 OpenAPI(仅 artifact 有);③ 缓存只在 calendar。

### 1.2 目标

1. 第三方开发者能**几分钟内**注册应用、拿到凭证、发出第一个成功请求。
2. 一个**精选、版本化、稳定**的公开只读 API 子集(绝不暴露 internal/admin/写)。
3. 自助门户:应用管理、凭证(show-once)、用量/配额、OpenAPI 交互文档。
4. 按 key/应用的**限流 + 配额 + 分层**,公开读**可被 Cloudflare 边缘缓存**。
5. **复用 IdP**:开发者账号 = kungal 账号;应用 = `oauth_clients` 行;不另造认证系统、不上网关。
6. NSFW 默认关闭,按 scope / tier 显式闸控(ToS / 合规)。

### 1.3 非目标(v1)

- 不开放写 / admin 端点(投稿类写操作放 Phase 3,走 OAuth2 + 用户授权)。
- 不引入 Kong/Tyk/Gravitee 等网关(运维重、门户多在付费版;我们规模 + 已有件不划算)。
- 不做付费 / 计费(仅做配额与分层的"业务上限",变现留待将来)。
- MCP server 仅"留位"(见 §13 Phase 4)。

---

## 2. 域名与部署拓扑

三个域名,各自单独职责(均在 Cloudflare 后、Traefik 路由):

| 角色 | 建议域名 | 后端 | 库 |
|---|---|---|---|
| 鲲 Galgame API(对外只读) | `api.kungal.org` | galgame 服务(`cmd/galgame`) | `kun_galgame_wiki` |
| 开发者门户 | `developer.kungal.org` | 门户前端(Nuxt) + 平台后端(扩展 account/IdP 侧) | `kun_galgame_infra` |
| IdP(已存在) | 现有 oauth 域名 | `cmd/oauth` | `kun_galgame_infra` |

```
                         ┌────────────── Cloudflare(TLS + 边缘缓存) ──────────────┐
   第三方应用 ──────────▶ │  api.kungal.org   developer.kungal.org   <oauth 域名>   │
   (服务端持 API Key       └──────┬───────────────────┬────────────────────┬────────┘
    或 OAuth2 token)              │                   │                    │
                              Traefik              Traefik              Traefik
                                  │                   │                    │
                         galgame 服务(API)     门户前端 + 平台后端       IdP(cmd/oauth)
                         kun_galgame_wiki        kun_galgame_infra      kun_galgame_infra
                                  │                   │  └── oauth_clients / developer_api_keys / usage
                                  └── 中间件:鉴权 → 限流 → 配额 → scope → content_limit
                                       (API key 经 IdP introspection,Redis 缓存)
```

**为什么 API 用独立域名(`api.kungal.org`)**:
- 与站点解耦,可施加**独立的缓存 / 限流 / CORS / WAF** 策略。
- 公开只读响应可在 **Cloudflare 边缘缓存**(见 §8),把第三方流量挡在边缘,wiki 几乎不被打——这是"开放 API 代价可控"的关键。
- 给第三方一个**稳定、版本化**的入口,与内部站点域名的演进解耦。
- 现实约束:wiki API 本就可能独立部署到单独域名,本设计与之一致。

---

## 3. 公开 API 子集(精选 + 版本化)

### 3.1 原则

- **白名单暴露**:只把精选的只读端点放进 `api.kungal.org/v1/…`;internal / admin / 写端点**永不**进入公开路由(物理上不挂到 API 服务的公开路由组)。
- **URL 版本化** `/v1/`:一旦有了无法协调破坏性变更的外部开发者,版本化与弃用策略从"过早优化"变成"硬需求"。
- **弃用策略**:破坏性变更必须升 `/v2/`;字段级弃用走 `Deprecation` / `Sunset` 响应头 + 门户公告 + 不少于 N 个月窗口。

### 3.2 v1 端点清单(草案)

| 公开端点(`/v1`) | 映射内部 | scope | 说明 |
|---|---|---|---|
| `GET /v1/galgame` | `GET /galgame`(List) | `galgame:read` | 分页/排序/搜索/发售范围;**改游标分页**(见 §8 备注) |
| `GET /v1/galgame/{id}` | `GET /galgame/:gid` | `galgame:read` | 详情 |
| `GET /v1/galgame/batch` | `GET /galgame/batch` | `galgame:read` | 批量(brief/detail) |
| `GET /v1/galgame/search` | `GET /galgame/search` | `galgame:read` | Meilisearch |
| `GET /v1/galgame/calendar*` | calendar 三件套 | `galgame:read` | 已有 ETag/缓存,直接复用 |
| `GET /v1/official` `…/{id}` `…/{id}/galgames` | official List/Get/members | `official:read` | 会社目录 + 成员 |
| `GET /v1/tag` `…/{id}` `…/{id}/galgames` | tag | `tag:read` | |
| `GET /v1/engine` / `GET /v1/series` … | engine/series | `taxonomy:read` | |
| (Phase 3)`POST /v1/galgame/{id}/submit` 等 | 投稿/PR | `galgame:submit` | 需 OAuth2 用户授权 |

> 不进入公开路由:`/admin/*`、`/:gid/revert`、直接写、消息队列、site 管理等。

### 3.3 稳定性承诺

- 已发布字段不删不改语义;只做**向后兼容**的新增。
- 公开 `content_limit` 语义统一(见 §11);各端点默认 = `sfw`。

---

## 4. 认证与授权

两条腿,按风险分:

### 4.1 API Key —— 主入口,面向"读公开目录"

- **格式**:`kg_live_<base62(24B)>` / `kg_test_<base62(24B)>`(前缀区分环境;前缀便于密钥泄漏扫描器识别)。
- **存储**(复用 `oauth_client.go` 的 `HashOAuthClientSecret` 模式):
  - 库里**只存 `sha256(key)` 的 hex**,带 `sha256:` 前缀;**明文仅创建时显示一次**,永不落库。
  - 另存 `key_prefix`(如 `kg_live_a1b2`)与 `last4` 供门户识别。
  - 校验用 `crypto/subtle` 常量时间比较(同 `VerifySecret`)。
- **传递**:`Authorization: Bearer kg_live_…`(统一用 Authorization;`X-API-Key` 作兼容备选)。
- **一个应用可有多把 key**:支持**轮换**(签发新 key,旧 key 设未来 `expires_at`,宽限 24–72h,不瞬杀)与**吊销**(`revoked_at`,下次请求即拒)。
- **默认 scope** = `galgame:read`(只读公开);NSFW 需单独 scope + 更高 tier。
- API key 是**机密**:只能服务端使用;浏览器直连第三方用 OAuth2 public client + PKCE,**不发 key**。

### 4.2 OAuth2 —— 写 / 代表用户的操作,扩展 IdP

- **应用 = `oauth_clients` 行**(第三方:`AutoConsent=false` → 必看同意页;SPA 用 `IsPublic=true` + PKCE)。
- 复用现有 grant 白名单(`Grants`)+ scope 白名单(`AllowedScopes` / `CheckScope`):
  - `client_credentials`:应用自身(app-only)读受限资源。
  - `authorization_code` + PKCE:**代表某 kungal 用户**(如代为投稿)。
- **scope 词表**(起步最小,可扩展):
  - `galgame:read` `official:read` `tag:read` `taxonomy:read`(公开读)
  - `galgame:nsfw`(放开 NSFW,需 tier 批准)
  - `galgame:submit` `user:read`(Phase 3)

### 4.3 校验路径(galgame API 侧)

galgame 服务**不直接读** `kun_galgame_infra`;凭证在 API 服务边缘解析:

- **Bearer JWT**(OAuth2):本地验签(galgame 已对一方 token 这么做)→ 取 `client_id` + `scope`。
- **API Key**:调 IdP 的内部 introspection 端点 → **Redis 缓存**结果(短 TTL,如 60s),避免每请求打 IdP。

introspection 契约(IdP 新增,内部 s2s):
```
POST /oauth/apikey/introspect          (s2s, 仅内网/带 s2s 凭证)
  { "key": "kg_live_…" }
→ 200 { "active": true, "client_id": "...", "app_name": "...",
        "scopes": ["galgame:read"], "tier": "free",
        "nsfw_allowed": false, "key_id": 123, "rate_per_min": 60, "quota_daily": 50000 }
→ 200 { "active": false }   // 未知/已吊销/已过期
```
> 备选:若与 image/artifact 现有的 client 校验机制(site-key)一致地"共享 DB 读",可改为 galgame 服务直读 `oauth_clients`/`developer_api_keys`——**实现时与现有 image/artifact 的 client 校验路径对齐**,二选一,保持一致。

---

## 5. 数据模型(均在主库 `kun_galgame_infra`)

### 5.1 扩展 `oauth_clients`(沿用 Image*/Artifact* 扩展字段范式)

应用(无论 API-key-only 还是 OAuth2)都是一行 `oauth_clients`,新增:

```go
// --- Developer platform / 鲲 Galgame API extension fields ---
// OwnerUserID: 第三方开发者应用的拥有者(kungal 账号)。一方站点 client 为 NULL
// (它们靠 SiteID 归属)。门户的 "我的应用" 按此过滤,也用于管理鉴权。
OwnerUserID    *uint  `gorm:"index" json:"owner_user_id,omitempty"`

GalgameEnabled bool   `gorm:"not null;default:false" json:"galgame_enabled"` // 准入 鲲 Galgame API
GalgameTier    string `gorm:"size:20;not null;default:'free'" json:"galgame_tier"` // free|partner|internal
GalgameNSFWAllowed bool `gorm:"not null;default:false" json:"galgame_nsfw_allowed"`
// 限流/配额(0 = 用 tier 默认值,见 §7)
GalgameRatePerMin  int `gorm:"not null;default:0" json:"galgame_rate_per_min"`
GalgameQuotaDaily  int `gorm:"not null;default:0" json:"galgame_quota_daily"`
```
> scope 直接复用既有 `AllowedScopes` + `CheckScope`,不另起字段。

### 5.2 新表 `developer_api_keys`

```go
type DeveloperAPIKey struct {
    ID          uint       `gorm:"primaryKey" json:"id"`
    ClientID    string     `gorm:"size:50;not null;index" json:"client_id"` // FK oauth_clients.id(= 应用)
    Name        string     `gorm:"size:100;not null" json:"name"`           // 开发者起的标签
    KeyHash     string     `gorm:"size:80;not null;uniqueIndex" json:"-"`   // "sha256:<hex>"
    KeyPrefix   string     `gorm:"size:24;not null;index" json:"key_prefix"`// kg_live_a1b2
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

> **迁移**:以上列 + 表都在 `kun_galgame_infra` → `go run ./cmd/migrate`(部署不自动跑,见 §14)。

---

## 6. 请求生命周期(API 中间件链)

```
Cloudflare(TLS,可能命中边缘缓存→直接返回) 
  → Traefik 
    → 鲲 Galgame API:
       1. resolveCredential:  X-API-Key/Bearer → app + scopes + tier + nsfw_allowed
                              (JWT 本地验;API key 经 introspection + Redis 缓存)
                              失败 → 401
       2. rateLimit(Redis,滑动窗口,key=app/key) → 超 → 429 + Retry-After + X-RateLimit-*
       3. quota(Redis 当日计数器) → 超 → 429 + X-Quota-*
       4. requireScope(端点所需 scope ⊆ 凭证 scope) → 缺 → 403
       5. content_limit 闸:默认 sfw;请求 nsfw 需 galgame:nsfw scope + nsfw_allowed,否则降级为 sfw 或 403
       6. handler(命中 ETag → 304;否则查询 + 设缓存头)
       7. async:Redis incr 用量 + last_used_at;(周期 flush 落库)
```

伪代码(中间件):
```go
func KunGalgameAPIAuth(c fiber.Ctx) error {
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

**两件不同的事**:限流 = 短期防滥用(req/min);配额 = 业务上限(req/day)。

| tier | rate/min | quota/day | NSFW | 适用 |
|---|---|---|---|---|
| `free` | 60 | 50,000 | 否 | 默认,自助注册即得 |
| `partner` | 600 | 1,000,000 | 可申请 | 审批的合作方 |
| `internal` | 不限 | 不限 | 是 | 一方应用(forum/moyu/wiki) |

- Redis 实现:限流用滑动窗口(`ratelimit:{key}:{minute}`),配额用当日计数(`quota:{key}:{YYYY-MM-DD}`,TTL 到次日)。
- 响应头:`X-RateLimit-Limit/Remaining/Reset`、`Retry-After`、`X-Quota-Limit/Remaining`。门户实时显示剩余配额。
- 应用/ key 上的 `GalgameRatePerMin`/`GalgameQuotaDaily` 为 0 时用 tier 默认值(同 `RefreshTokenTTL()` 的"0=用默认"范式)。

---

## 8. 缓存(公开读的承重墙)

**关键设计:把"鉴权"与"响应内容"解耦,让公开读对 Cloudflare 可缓存。**

- 同一 `content_limit` + 同一版本下,公开目录读的**响应内容对所有调用者相同**(与是哪把 key 无关)。鉴权只用于**限流/计量**,不改变响应体。
- 因此缓存键 = `(path, query, content_limit, /v1)`,**不含 key**;响应可带 `Cache-Control: public, s-maxage=…`,被 Cloudflare 边缘共享缓存。
- 把 calendar 已验证的模式铺到 list/detail/batch/官方成员:
  - 弱 **ETag**(嵌 `max(updated)` 或资源指纹)→ `If-None-Match` 命中回 304。
  - `Cache-Control`:历史/稳定数据 `s-maxage` 长(如 1 天),易变的短(如 5 分钟);`max-age=0` 让浏览器每次回源校验。
  - `Cache-Tag`(Cloudflare Cache Rules / 按内容键)便于精准失效。
- 鉴权失败 / 配额头等**不可缓存**部分,仍在回源层处理(CF 仅缓存 2xx 公开读)。
- **备注(吸收自 API 设计 skill,用在对的地方)**:`GET /v1/galgame` 列表在 6 万→10 万+ 目录上,offset 深翻页有性能悬崖 → 公开列表改 **游标分页**(`cursor`/`next_cursor`),既稳又对缓存友好。

> 这一节是"开放 API 代价可控"的核心:做好缓存 + CF,绝大多数公开读在边缘命中,wiki 不被打爆。

---

## 9. 开发者门户(`developer.kungal.org`)

- **账号复用**:用 kungal 账号经 IdP 登录即开发者账号,**不另造身份**。
- **核心功能**:
  1. 创建应用(= 一行 `oauth_clients`,`owner_user_id=当前用户`,`galgame_enabled=true`)→ 拿 `client_id`(OAuth)。
  2. 管理 **API Keys**:创建(**show-once** 明文)、看 `prefix+last4+last_used`、轮换(带宽限)、吊销。
  3. **用量/配额**:实时剩余 + 历史曲线(读 `developer_api_usage`)。
  4. **OpenAPI 文档**:用 **Scalar** 渲染(MIT、Try-It 最强、支持 OAuth flow、可嵌 Nuxt)。
  5. 申请更高 tier / NSFW(走审批)。
- **技术**:门户前端 Nuxt(`apps/` 下新增或并入现有);平台后端扩展 account/IdP 侧的 API(应用/ key/ 用量 CRUD,鉴权用现有 JWT + `owner_user_id` 归属校验)。

---

## 10. OpenAPI 策略

- galgame 现为纯 Fiber,无 spec。两条路:
  1. **渐进 Huma 化公开只读端点**(artifact 的 `cmd/gen-openapi` 是现成样板)——code-first,spec 自动跟代码走,最不易漂。**推荐**。
  2. 基于现有 Markdown 契约 authored 一份 OpenAPI + 加 verify(类似 `docs:verify`)守 spec==code。
- 产出 `api.kungal.org/openapi.json`(或 `docs/api/openapi.yaml`)→ 门户 Scalar 渲染 → 第三方据此生成 SDK。
- 公开后,`docs/api/` 升级为 **Tier-A 对外契约**,纳入 `docs:verify`。

---

## 11. 安全 / 滥用 / 合规

- **HTTPS 强制**(Cloudflare);key 只走 header,**不进 URL、不进日志**(日志只留 `key_prefix`)。
- **NSFW**:默认 `sfw`;放开需 `galgame:nsfw` scope + `nsfw_allowed` tier,并审计——galgame 的 NSFW 闸控从"整洁问题"升级为**合规问题**(ToS / 法律)。
- **CORS**:`api.kungal.org` 对浏览器直连**不开放任意 origin 携带 API key**(key 是机密,仅服务端);浏览器场景走 OAuth2 public client + PKCE。
- **ToS / 滥用**:服务条款 + 异常用量告警 + 一键吊销 key/应用。
- **审计**:key 创建/轮换/吊销、tier 变更、异常 4xx/5xx 速率,写审计日志。

---

## 12. 可观测 / 计量

- Redis 实时计数(限流/配额)→ 周期 flush `developer_api_usage` → 门户曲线 + 配额执行 + 告警。
- 每请求:`last_used_at` 异步回写;按 (client, key, day) 聚合 count/4xx/5xx。

---

## 13. 分期

| 阶段 | 内容 | 状态 |
|---|---|---|
| **Phase 1 地基** | 精选公开只读子集 + `/v1` + OpenAPI(Huma 化只读端点)+ Scalar 文档 + **API Key**(hash/show-once/轮换/吊销)+ Redis 限流 + **热路径缓存 + Cloudflare** + `api.kungal.org` / `developer.kungal.org` 域名 | ⬜ 待实施 |
| **Phase 2** | 配额/分层 + 用量面板 + 门户打磨 + **OAuth2 client_credentials** + scope 词表 | ⬜ |
| **Phase 3** | `authorization_code`+PKCE **代表用户**(投稿/写)+ `galgame:nsfw` tier 闸 + 审批流 | ⬜ |
| **Phase 4(可选·现代)** | 把公开只读 API 同时暴露成 **MCP server**,让 AI 助手/agent 直接查 galgame 目录 | ⬜ 留位 |

> 依赖关系:Phase 1 里**缓存 + OpenAPI 是前置地基**(不是优化项)——没有缓存,"所有读走 API" 会把 wiki 打成瓶颈;没有 OpenAPI,门户文档/SDK 无从谈起。

---

## 14. 迁移与运维提醒

- **数据库迁移(主库 `kun_galgame_infra`)**:新增 `oauth_clients` 列(`owner_user_id` + `galgame_*`)+ 新表 `developer_api_keys` / `developer_api_usage` → **`go run ./cmd/migrate`**。⚠️ 部署**不自动**跑迁移,漏跑 = GORM 读不存在的列 → 静默失败(参见仓库历史教训)。
- **新域名**:`api.kungal.org`、`developer.kungal.org` → DNS + Cloudflare(含公开读的 Cache Rules)+ Traefik 路由 + 各后端 CORS allowlist。
- **Redis**:新增 `ratelimit:*` / `quota:*` / `apikey:*`(introspection 缓存)键空间。
- **契约**:公开后 `docs/api/` 纳入 `docs:verify`,成为对外 Tier-A 契约。

---

## 15. 关键决策 / 待定

1. **API 域名定名**:`api.kungal.org`(本设计采用)还是其它?
2. **API key 校验机制**:IdP introspection(解耦)还是 galgame 直读 DB(与 image/artifact 的 site-key 校验对齐)——按现有机制取齐。
3. **OpenAPI 落地**:Huma 化只读端点(推荐)vs authored spec + verify。
4. **公开列表分页**:offset → 游标(推荐,配合缓存与大目录)。
5. **用量计量粒度**:仅按 (key, day) 计数,还是同时按端点维度(成本 vs 洞察)。
6. **NSFW 开放策略**:是否对外开放、以何 tier/审批门槛(合规决定)。
