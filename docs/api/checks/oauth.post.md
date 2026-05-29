# OAuth 服务 — POST API 清单

> 服务: **oauth**（`apps/api/cmd/oauth`） · Base URL: `/api/v1` · 路由源: `cmd/oauth/main.go`
>
> 鉴权/状态图例见 [README](./README.md)。配套: [oauth.get.md](./oauth.get.md) · [oauth.put.md](./oauth.put.md) · [oauth.delete.md](./oauth.delete.md) · [oauth.patch.md](./oauth.patch.md)
>
> **审计完成** —— 🔧 已修 / ✅ 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 统计

- 本服务 POST 端点：**21**
  - 认证（公开）6 · 认证（自助）3 · OAuth 协议 3 · 用户 1 · 管理-用户 5 · 管理-站点/客户端 2 · 管理-任务 1
- 注：`strict` = 严格限流中间件（非鉴权）。

---

## 1. 认证 — 公开（注册 / 登录 / 找回）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/auth/register/send-code` | 🌐 +strict | `authH.SendRegisterCode` | ✅ | 注册邮箱验证码 |
| `POST /api/v1/auth/register` | 🌐 +strict | `authH.Register` | ✅ | 注册（需验证码）|
| `POST /api/v1/auth/login` | 🌐 +strict | `authH.Login` | ✅ | 登录（封禁用户拒）|
| `POST /api/v1/auth/refresh` | 🌐 | `authH.Refresh` | 🔧 | 用 httpOnly refresh cookie 续 access_token；#11 封禁用户拒发新 token 并撤销会话 |
| `POST /api/v1/auth/password/forgot` | 🌐 +strict | `authH.ForgotPassword` | ✅ | 发重置邮件 |
| `POST /api/v1/auth/password/reset` | 🌐 +strict | `authH.ResetPassword` | 🔧 | 用 token 重置；#27 基础设施错误→500(非误导性400)；#28 先原子消费 token，消除重放窗口 |

## 2. 认证 — 自助（登录态）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/auth/logout` | 🔒 | `authH.Logout` | ✅ | |
| `POST /api/v1/auth/email/send-code` | 🔒 | `authH.SendEmailChangeCode` | ✅ | 改邮箱验证码 |
| `POST /api/v1/auth/me/avatar` | 🔒 | `avatarUploadH.UploadMine` | ✅ | 仅 image client 配置时注册 |

## 3. OAuth 2.0 协议

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/oauth/token` | 🔑 | `oauthH.Token` | ✅ | token 端点（client 凭证 + 限流 `oauthTokenLimiter`）；含授权码兑换/refresh grant，已加 banned 检查 |
| `POST /api/v1/oauth/revoke` | 🌐 | `oauthH.Revoke` | ✅ | 吊销 token（凭 token 本身）|
| `POST /api/v1/oauth/authorize/consent` | 🔒 | `oauthH.Consent` | ✅ | 同意授权 → 下发 code |

## 4. 用户（服务到服务）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/users/:id/moemoepoint` | 🔑 | `moemoepointH.Adjust` | ✅ | 发放/扣除（幂等）；s2s 不可用 admin_*/migration reason |

## 5. 管理 — 用户

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/admin/users/:uuid/ban` | ⚙️ | `adminH.BanUser` | ✅ | 封禁 + 清会话 |
| `POST /api/v1/admin/users/:uuid/unban` | ⚙️ | `adminH.UnbanUser` | ✅ | 解封（拒已匿名化）|
| `POST /api/v1/admin/users/:uuid/anonymize` | ⚙️ | `adminH.AnonymizeUser` | ✅ | PII 清洗 + 封禁 + 头像 GC（不可逆）|
| `POST /api/v1/admin/users/:uuid/moemoepoint` | ⚙️ | `moemoepointH.AdminAdjust` | ✅ | 管理员发放/扣除（reason 按 delta 正负派生）|
| `POST /api/v1/admin/users/:uuid/avatar` | ⚙️ | `avatarUploadH.Upload` | ✅ | 仅 image client 配置时注册 |

## 6. 管理 — 站点 / OAuth 客户端

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/sites` | ⚙️ | `siteH.Create` | ✅ | |
| `POST /api/v1/oauth/clients` | ⚙️ | `siteH.CreateClient` | 🔧 | #32 grants 校验(非空+枚举子集) |

## 7. 管理 — 任务

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/admin/jobs/:name/run` | ⚙️ | inline（`registerJobsAdmin`）| ✅ | 手动触发 job（后台运行）|
