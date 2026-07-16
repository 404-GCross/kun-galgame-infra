# Galgame Wiki 服务 — PUT API 清单

> 服务: **galgame 面**（宿主 `cmd/catalog`,装配 `apps/api/internal/galgameapp`;独立 `galgame` 二进制已于 wiki 退役 W5 移除） · Base URL: `/api` · 路由源: `internal/galgameapp/mount.go`
>
> 图例见 [README](./README.md)。配套: [galgame.get.md](./galgame.get.md) · [galgame.post.md](./galgame.post.md) · [galgame.delete.md](./galgame.delete.md) · [galgame.patch.md](./galgame.patch.md)
>
> **审计完成** —— 已修 / 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 统计

- 本服务 PUT 端点：**8**（Galgame 3 · 管理 1 · Tag 1 · Official 1 · Engine 1 · Series 1）

---

## 1. Galgame

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PUT /api/galgame/:gid` | 登录 | `galgameH.Update` | 已修 | 直接更新（写 revision）；#20 dive 校验(handler 加 Validate)；#21 vndb_id 格式/唯一校验；#38 草稿(3/4)非 admin 走 PATCH |
| `PUT /api/galgame/:gid/prs/:id/merge` | 登录 | `revisionH.MergePR` | 已修 | 合并 PR（角色校验在 handler 内）；#39 completed_time=NOW()；#40 gid 作用域；#41 快照切片顺序无关比较(消除伪字段冲突) |
| `PUT /api/galgame/:gid/prs/:id/decline` | 登录 | `revisionH.DeclinePR` | 已修 | 拒绝 PR；#40 gid 作用域 |

## 2. 管理（admin / moderator）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PUT /api/admin/galgame/:gid/status` | admin/mod | `adminH.UpdateGalgameStatus` | 已审计 | 改状态（发布/封禁/拒绝）|

## 3. 分类轴（Tag / Official / Engine / Series）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `PUT /api/tag` | 登录 | `tagH.Update` | 已审计 | |
| `PUT /api/official` | 登录 | `officialH.Update` | 已审计 | |
| `PUT /api/engine` | 登录 | `engineH.Update` | 已审计 | |
| `PUT /api/series/:id` | 登录 | `seriesH.Update` | 已修 | #10 加 admin/moderator 角色门 |
