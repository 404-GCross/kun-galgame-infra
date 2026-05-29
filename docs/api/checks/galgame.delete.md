# Galgame Wiki 服务 — DELETE API 清单

> 服务: **galgame**（`apps/api/cmd/galgame`） · Base URL: `/api` · 路由源: `cmd/galgame/main.go`
>
> 图例见 [README](./README.md)。配套: [galgame.get.md](./galgame.get.md) · [galgame.post.md](./galgame.post.md) · [galgame.put.md](./galgame.put.md) · [galgame.patch.md](./galgame.patch.md)
>
> **inventory 阶段** —— 状态列 ⏳。

## 统计

- 本服务 DELETE 端点：**8**（Galgame 关系/草稿 4 · Tag 1 · Official 1 · Engine 1 · Series 1）

---

## 1. Galgame — 关系子资源 / 草稿（登录）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /api/galgame/:gid/links` | 🔒 | `linkH.DeleteLink` | ⏳ | |
| `DELETE /api/galgame/:gid/aliases` | 🔒 | `linkH.DeleteAlias` | ⏳ | |
| `DELETE /api/galgame/:gid/contributors/:id` | 🔒 | `contributorH.Delete` | ⏳ | |
| `DELETE /api/galgame/:gid` | 🔒 | `submissionH.DeleteDraft` | ⏳ | 撤回自己的草稿投稿 |

## 2. 分类轴（Tag / Official / Engine / Series）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /api/tag/:id` | 🔒 | `tagH.Delete` | ⏳ | |
| `DELETE /api/official/:id` | 🔒 | `officialH.Delete` | ⏳ | |
| `DELETE /api/engine/:id` | 🔒 | `engineH.Delete` | ⏳ | |
| `DELETE /api/series/:id` | 🔒 | `seriesH.Delete` | ⏳ | |
