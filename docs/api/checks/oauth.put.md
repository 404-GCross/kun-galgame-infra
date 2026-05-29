# OAuth 服务 — PUT API 清单

> 服务: **oauth**（`apps/api/cmd/oauth`） · Base URL: `/api/v1` · 路由源: `cmd/oauth/main.go`
>
> 图例见 [README](./README.md)。配套: [oauth.get.md](./oauth.get.md) · [oauth.post.md](./oauth.post.md) · [oauth.delete.md](./oauth.delete.md) · [oauth.patch.md](./oauth.patch.md)
>
> **inventory 阶段** —— 状态列 ⏳。

## 统计

- 本服务 PUT 端点：**4**（认证-自助 2 · 管理-站点/客户端 2）

---

## 1. 认证 — 自助（登录态）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PUT /api/v1/auth/password` | 🔒 | `authH.ChangePassword` | ⏳ | 改密码 |
| `PUT /api/v1/auth/email` | 🔒 | `authH.ChangeEmail` | ⏳ | 改邮箱（需验证码）|

## 2. 管理 — 站点 / OAuth 客户端

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PUT /api/v1/sites/:id` | ⚙️ | `siteH.Update` | ⏳ | |
| `PUT /api/v1/oauth/clients/:id` | ⚙️ | `siteH.UpdateClient` | ⏳ | |
