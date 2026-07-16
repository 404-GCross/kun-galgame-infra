# Galgame Wiki 服务 — DELETE API 清单

> 服务: **galgame 面**（宿主 `cmd/catalog`,装配 `apps/api/internal/galgameapp`;独立 `galgame` 二进制已于 wiki 退役 W5 移除） · Base URL: `/api` · 路由源: `internal/galgameapp/mount.go`
>
> 图例见 [README](./README.md)。配套: [galgame.get.md](./galgame.get.md) · [galgame.post.md](./galgame.post.md) · [galgame.put.md](./galgame.put.md) · [galgame.patch.md](./galgame.patch.md)
>
> **审计完成** —— 已修 / 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 统计

- 本服务 DELETE 端点：**8**（Galgame 关系/草稿 4 · Tag 1 · Official 1 · Engine 1 · Series 1）

---

## 1. Galgame — 关系子资源 / 草稿（登录）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /api/galgame/:gid/links` | 登录 | `linkH.DeleteLink` | 已修 | #08 owner/admin 越权门 |
| `DELETE /api/galgame/:gid/aliases` | 登录 | `linkH.DeleteAlias` | 已修 | #08 owner/admin 越权门 |
| `DELETE /api/galgame/:gid/contributors/:id` | 登录 | `contributorH.Delete` | 已审计 | |
| `DELETE /api/galgame/:gid` | 登录 | `submissionH.DeleteDraft` | 已审计 | 撤回自己的草稿投稿 |

## 2. 分类轴（Tag / Official / Engine / Series）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /api/tag/:id` | 登录 | `tagH.Delete` | 已审计 | |
| `DELETE /api/official/:id` | 登录 | `officialH.Delete` | 已审计 | |
| `DELETE /api/engine/:id` | 登录 | `engineH.Delete` | 已审计 | |
| `DELETE /api/series/:id` | 登录 | `seriesH.Delete` | 已审计 | |
