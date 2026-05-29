# OAuth 服务 — PUT API 清单

> 服务: **oauth**（`apps/api/cmd/oauth`） · Base URL: `/api/v1` · 路由源: `cmd/oauth/main.go`
>
> 图例见 [README](./README.md)。配套: [oauth.get.md](./oauth.get.md) · [oauth.post.md](./oauth.post.md) · [oauth.delete.md](./oauth.delete.md) · [oauth.patch.md](./oauth.patch.md)
>
> **审计完成** —— 🔧 已修 / ✅ 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 统计

- 本服务 PUT 端点：**4**（认证-自助 2 · 管理-站点/客户端 2）

---

## 1. 认证 — 自助（登录态）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PUT /api/v1/auth/password` | 🔒 | `authH.ChangePassword` | ✅ | 改密码 |
| `PUT /api/v1/auth/email` | 🔒 | `authH.ChangeEmail` | ✅ | 改邮箱（需验证码）|

## 2. 管理 — 站点 / OAuth 客户端

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PUT /api/v1/sites/:id` | ⚙️ | `siteH.Update` | ✅ | |
| `PUT /api/v1/oauth/clients/:id` | ⚙️ | `siteH.UpdateClient` | 🔧 | #32 grants 校验 |
