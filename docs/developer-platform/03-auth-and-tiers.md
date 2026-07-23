# 认证、授权与分层

> 本文承载 §4 认证与授权(API Key / OAuth2 / scope / 校验路径)、§7 限流 + 配额 + 分层。设计与命名约定见 [01-design.md](./01-design.md);数据模型(`developer_api_keys` 等)见 [04 §5](./04-platform-internals.md)。

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

### 4.4 `/dev/*` 自助面的 client 栅栏(confused-deputy 硬前置)

开发者门户自助面(`/dev/*`,cmd/oauth)用**用户 JWT** 鉴权(不是 API key)。`middleware.Auth` 只验签名 + 用户在册未封,**不验这枚 access token 属于哪个 OAuth client**。若无额外栅栏,任何第三方 OAuth app 拿到的用户 token(哪怕只授了 `openid profile email`)都能替用户在 `/dev/*` 铸/轮换/吊销 API key——典型 confused-deputy。

因此 `middleware.Auth` 之后加一道 **`DevPortalFence`**(RFC 9068 的 `client_id` claim,pkg/utils/jwt.go 第一方为空):

- `client_id == ""`(第一方 `/auth/login` session token,无 OAuth client)→ **放行**;
- `client_id` ∈ 允许列表 → **放行**(即开发者门户自己的 confidential client);
- 其余 → **403** + `slog.Warn` 记被拒的 `client_id`(不记 token)。

**允许列表 = env `KUN_DEV_PORTAL_CLIENT_IDS`(CSV)**。**空 = fail-closed**:只放行第一方 token,任何 client token 一律 403(与 trust 侧 `KUN_TRUST_FORWARDER_CLIENT_IDS` 同形)。门户以 auth-code + PKCE 让用户登录时,token 带门户自己的 `client_id`,故门户 client 注册后须把它填进这个 env,否则门户用户会被自家栅栏 403。设在 **oauth 服务**上。

> **升级路径(第三方实际开放时)**:本栅栏是"只让门户自己进"的硬前置,不是细粒度授权。将来对任意第三方开放 `/dev/*` 时,新增 `dev:manage` scope + 同意页文案(第三方 app 须显式获用户同意才能管理其开发者资源),届时栅栏从"client 白名单"升级为"白名单 ∪ 持 `dev:manage` 的已同意 client"。本波不做。

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
