# Galgame Wiki 服务 — POST API 清单

> 服务: **galgame**（`apps/api/cmd/galgame`） · Base URL: `/api` · 路由源: `cmd/galgame/main.go`
>
> 图例见 [README](./README.md)。配套: [galgame.get.md](./galgame.get.md) · [galgame.put.md](./galgame.put.md) · [galgame.delete.md](./galgame.delete.md) · [galgame.patch.md](./galgame.patch.md)
>
> **审计完成** —— 🔧 已修 / ✅ 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 统计

- 本服务 POST 端点：**17**
  - Galgame（编辑/投稿）7 · 管理 1 · Tag 2 · Official 2 · Engine 2 · Series 3

---

## 1. Galgame — 编辑 / 投稿（登录）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/galgame` | 🛡️ | `galgameH.Create` | 🔧 | 管理员直发（绕过审核）；#20 covers/screenshots 元素校验(dive + hex hash) |
| `POST /api/galgame/:gid/revert` | 🔒 | `revisionH.Revert` | ✅ | 回滚到某版本 |
| `POST /api/galgame/:gid/prs` | 🔒 | `revisionH.SubmitPR` | 🔧 | 提交编辑 PR；#07 title/message 不再被丢弃(模型/DTO 拆分) |
| `POST /api/galgame/:gid/links` | 🔒 | `linkH.CreateLink` | 🔧 | #08 owner/admin 越权门；#42 不存在 gid→404 |
| `POST /api/galgame/:gid/aliases` | 🔒 | `linkH.CreateAlias` | 🔧 | #08 越权门；#42 不存在 gid→404 |
| `POST /api/galgame/submit` | 🔒 | `submissionH.Submit` | ✅ | 用户投稿（status=3 待审）|
| `POST /api/galgame/:gid/claim` | 🔒 | `submissionH.Claim` | ✅ | 认领 VNDB 草稿 |

## 2. 管理（admin / moderator）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/admin/galgame/ban-by-user/:userId` | 🛡️ | `adminH.BanGalgamesByUser` | ✅ | 批量软删某用户全部 galgame（spam 清理）|

## 3. Tag

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/tag` | 🔒 | `tagH.Create` | ✅ | 任何登录用户可新建（补 VNDB 缺失）|
| `POST /api/tag/:id/revert` | 🔒 | `taxRevH.TagRevert` | ✅ | |

## 4. Official（会社）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/official` | 🔒 | `officialH.Create` | ✅ | |
| `POST /api/official/:id/revert` | 🔒 | `taxRevH.OfficialRevert` | ✅ | |

## 5. Engine（引擎）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/engine` | 🔒 | `engineH.Create` | ✅ | |
| `POST /api/engine/:id/revert` | 🔒 | `taxRevH.EngineRevert` | ✅ | |

## 6. Series（系列）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/series` | 🔒 | `seriesH.Create` | 🔧 | #46 同名预检返回 400(非 500) |
| `POST /api/series/modal` | 🔒 | `seriesH.Modal` | ✅ | 弹窗用轻量创建/检索 |
| `POST /api/series/:id/revert` | 🔒 | `taxRevH.SeriesRevert` | ✅ | |
