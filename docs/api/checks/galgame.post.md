# Galgame Wiki 服务 — POST API 清单

> 服务: **galgame 面**（宿主 `cmd/catalog`,装配 `apps/api/internal/galgameapp`;独立 `galgame` 二进制已于 wiki 退役 W5 移除） · Base URL: `/api` · 路由源: `internal/galgameapp/mount.go`
>
> 图例见 [README](./README.md)。配套: [galgame.get.md](./galgame.get.md) · [galgame.put.md](./galgame.put.md) · [galgame.delete.md](./galgame.delete.md) · [galgame.patch.md](./galgame.patch.md)
>
> **审计完成** —— 已修 / 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 统计

- 本服务 POST 端点：**17**
  - Galgame（编辑/投稿）7 · 管理 1 · Tag 2 · Official 2 · Engine 2 · Series 3

---

## 1. Galgame — 编辑 / 投稿（登录）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/galgame` | admin/mod | `galgameH.Create` | 已修 | 管理员直发（绕过审核）；#20 covers/screenshots 元素校验(dive + hex hash) |
| `POST /api/galgame/:gid/links` | 登录 | `linkH.CreateLink` | 已修 | #08 owner/admin 越权门；#42 不存在 gid→404 |
| `POST /api/galgame/:gid/aliases` | 登录 | `linkH.CreateAlias` | 已修 | #08 越权门；#42 不存在 gid→404 |
| `POST /api/galgame/submit` | 登录 | `submissionH.Submit` | 已审计 | 用户投稿（status=3 待审）|
| `POST /api/galgame/:gid/claim` | 登录 | `submissionH.Claim` | 已审计 | 认领 VNDB 草稿 |

## 2. 管理（admin / moderator）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/admin/galgame/ban-by-user/:userId` | admin/mod | `adminH.BanGalgamesByUser` | 已审计 | 批量软删某用户全部 galgame（spam 清理）|

## 3. Tag

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/tag` | 登录 | `tagH.Create` | 已审计 | 任何登录用户可新建（补 VNDB 缺失）|
| `POST /api/tag/:id/revert` | 登录 | `taxRevH.TagRevert` | 已审计 | |

## 4. Official（会社）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/official` | 登录 | `officialH.Create` | 已审计 | |
| `POST /api/official/:id/revert` | 登录 | `taxRevH.OfficialRevert` | 已审计 | |

## 5. Engine（引擎）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/engine` | 登录 | `engineH.Create` | 已审计 | |
| `POST /api/engine/:id/revert` | 登录 | `taxRevH.EngineRevert` | 已审计 | |

## 6. Series（系列）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/series` | 登录 | `seriesH.Create` | 已修 | #46 同名预检返回 400(非 500) |
| `POST /api/series/modal` | 登录 | `seriesH.Modal` | 已审计 | 弹窗用轻量创建/检索 |
| `POST /api/series/:id/revert` | 登录 | `taxRevH.SeriesRevert` | 已审计 | |
