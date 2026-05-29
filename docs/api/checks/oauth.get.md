# OAuth 服务 — GET API 清单

> 服务: **oauth**（`apps/api/cmd/oauth`） · Base URL: `/api/v1`
>
> 路由源: `apps/api/cmd/oauth/main.go`（含 `registerJobsAdmin` / `registerImageAdmin` 两个 helper）
>
> 配套: [image.get.md](./image.get.md) · [galgame.get.md](./galgame.get.md) · [moderation.get.md](./moderation.get.md) · [artifact.get.md](./artifact.get.md)
>
> **本文件目前是 inventory 阶段** —— 仅列出全部 GET 端点供后续逐项审计，状态列暂为 ⏳。

## 图例 — 审计状态

- ✅ 已审计，对齐无问题
- 🔧 已审计，发现问题并修复
- ⏭️ 已审计，有意保持当前行为
- ⏳ 待审计（inventory 默认）

## 图例 — 鉴权

| 标记 | 中间件 | 含义 |
|---|---|---|
| 🌐 | （无） | 完全公开 |
| 🔐 | `OptionalJWT` | 可选鉴权；带 token 附加内容，匿名只看公共部分 |
| 🔒 | `Auth` | 必须登录。**oauth 的 `Auth` 会在每次请求查 DB user 状态**（封禁/匿名化即 403），非仅验签 |
| ⚙️ | `Auth` + `RequireRole("admin")` | 仅 admin |
| 🔑 | `OAuthClientBasicAuth` | 服务到服务（client_id:secret），非终端用户 JWT |

## 统计

- 本服务 GET 端点：**19**
  - 运维 1 · 认证/身份 1 · OAuth 协议 3 · 用户（s2s + self）5 · 管理-用户 3 · 管理-站点/客户端 4 · 管理-任务 2
- 注：`/admin/image/*` 物理上也跑在本进程，但归到 [image.get.md](./image.get.md) 审计。

---

## 0. 运维 / 健康

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/health` | 🌐 | inline | ⏳ | 健康检查 |

## 1. 认证 / 身份

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/auth/me` | 🔒 | `authH.Me` | ⏳ | 当前登录用户 profile |

## 2. OAuth 2.0 协议

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/oauth/authorize` | 🌐 | `oauthH.Authorize` | ⏳ | 授权入口；内部校验 session |
| `GET /api/v1/oauth/client-info` | 🌐 | `oauthH.GetClientPublic` | ⏳ | 客户端公开信息（前端决定是否显示同意页）|
| `GET /api/v1/oauth/userinfo` | 🔒 | `oauthH.UserInfo` | ⏳ | OIDC userinfo；按 scope 投影 |

## 3. 用户（服务到服务 + 自助）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/users/batch` | 🔑 | `userBatchH.Get` | ⏳ | 按 id 批量拉公开资料；不含 email/moemoepoint |
| `GET /api/v1/users/search` | 🔑 | `userBatchH.Search` | ⏳ | 用户名子串搜索 |
| `GET /api/v1/users/:id/moemoepoint` | 🔑 | `moemoepointH.GetBalance` | ⏳ | 余额（统一货币）|
| `GET /api/v1/users/:id/moemoepoint/log` | 🔑 | `moemoepointH.GetLog` | ⏳ | 流水**精简视图**（无 note/actor）|
| `GET /api/v1/users/:uuid` | 🔒 | `authH.GetProfile` | ⏳ | 按 uuid 取 profile |

## 4. 管理 — 用户

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/admin/users` | ⚙️ | `adminH.ListUsers` | ⏳ | 分页 + 搜索 |
| `GET /api/v1/admin/users/:uuid` | ⚙️ | `adminH.GetUser` | ⏳ | 含 session 数等 |
| `GET /api/v1/admin/users/:uuid/moemoepoint/log` | ⚙️ | `moemoepointH.AdminGetLog` | ⏳ | 流水**完整视图**（含 note/actor）|

## 5. 管理 — 站点 / OAuth 客户端

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/sites` | ⚙️ | `siteH.List` | ⏳ | |
| `GET /api/v1/sites/:id` | ⚙️ | `siteH.Get` | ⏳ | |
| `GET /api/v1/sites/:id/clients` | ⚙️ | `siteH.GetSiteClients` | ⏳ | 站点下的 OAuth 客户端 |
| `GET /api/v1/oauth/clients` | ⚙️ | `siteH.ListClients` | ⏳ | 全部客户端 |

## 6. 管理 — 任务（infra，跑在本进程）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/admin/jobs` | ⚙️ | inline（`registerJobsAdmin`）| ⏳ | 注册的 job + 各自最近一次 run（路由实参为空字符串）|
| `GET /api/v1/admin/jobs/:name/runs` | ⚙️ | inline | ⏳ | 某 job 的运行历史（`?limit`）|
