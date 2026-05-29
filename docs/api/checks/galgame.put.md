# Galgame Wiki 服务 — PUT API 清单

> 服务: **galgame**（`apps/api/cmd/galgame`） · Base URL: `/api` · 路由源: `cmd/galgame/main.go`
>
> 图例见 [README](./README.md)。配套: [galgame.get.md](./galgame.get.md) · [galgame.post.md](./galgame.post.md) · [galgame.delete.md](./galgame.delete.md) · [galgame.patch.md](./galgame.patch.md)
>
> **inventory 阶段** —— 状态列 ⏳。

## 统计

- 本服务 PUT 端点：**8**（Galgame 3 · 管理 1 · Tag 1 · Official 1 · Engine 1 · Series 1）

---

## 1. Galgame

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PUT /api/galgame/:gid` | 🔒 | `galgameH.Update` | ⏳ | 直接更新（写 revision）|
| `PUT /api/galgame/:gid/prs/:id/merge` | 🔒 | `revisionH.MergePR` | ⏳ | 合并 PR（角色校验在 handler 内）|
| `PUT /api/galgame/:gid/prs/:id/decline` | 🔒 | `revisionH.DeclinePR` | ⏳ | 拒绝 PR |

## 2. 管理（admin / moderator）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PUT /api/admin/galgame/:gid/status` | 🛡️ | `adminH.UpdateGalgameStatus` | ⏳ | 改状态（发布/封禁/拒绝）|

## 3. 分类轴（Tag / Official / Engine / Series）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PUT /api/tag` | 🔒 | `tagH.Update` | ⏳ | |
| `PUT /api/official` | 🔒 | `officialH.Update` | ⏳ | |
| `PUT /api/engine` | 🔒 | `engineH.Update` | ⏳ | |
| `PUT /api/series/:id` | 🔒 | `seriesH.Update` | ⏳ | |
