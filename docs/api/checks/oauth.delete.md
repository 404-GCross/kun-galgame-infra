# OAuth 服务 — DELETE API 清单

> 服务: **oauth**（`apps/api/cmd/oauth`） · Base URL: `/api/v1` · 路由源: `cmd/oauth/main.go`
>
> 图例见 [README](./README.md)。配套: [oauth.get.md](./oauth.get.md) · [oauth.post.md](./oauth.post.md) · [oauth.put.md](./oauth.put.md) · [oauth.patch.md](./oauth.patch.md)
>
> **inventory 阶段** —— 状态列 ⏳。

## 统计

- 本服务 DELETE 端点：**3**（管理-用户 1 · 管理-站点/客户端 2）
- 注：`/api/v1/admin/image/:hash` 物理上也跑在本进程，归到 [image.delete.md](./image.delete.md)。

---

## 1. 管理 — 用户

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /api/v1/admin/users/:uuid/sessions` | ⚙️ | `adminH.DeleteUserSessions` | ⏳ | 强制下线（清所有会话）|

## 2. 管理 — 站点 / OAuth 客户端

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /api/v1/sites/:id` | ⚙️ | `siteH.Delete` | ⏳ | |
| `DELETE /api/v1/oauth/clients/:id` | ⚙️ | `siteH.DeleteClient` | ⏳ | |
