# Galgame Wiki 服务 — GET API 清单

> 服务: **galgame**（`apps/api/cmd/galgame`） · Base URL: `/api`
>
> 路由源: `apps/api/cmd/galgame/main.go`
>
> 配套: [oauth.get.md](./oauth.get.md) · [image.get.md](./image.get.md) · [moderation.get.md](./moderation.get.md) · [artifact.get.md](./artifact.get.md)
>
> **inventory 阶段** —— 状态列暂为 ⏳。

## 图例 — 审计状态

- ✅ 已审计无问题 · 🔧 已修 · ⏭️ 有意保持 · ⏳ 待审计

## 图例 — 鉴权

| 标记 | 中间件 | 含义 |
|---|---|---|
| 🌐 | （无） | 完全公开 |
| 🔐 | `OptionalJWT` | 可选；带 token 附加自己的 pending/隐藏内容 |
| 🔒 | `JWTAuth` | 必须登录。**仅验签**（不查 DB 状态；与 oauth 的 `Auth` 不同）|
| 🛡️ | `JWTAuth` + `RequireRole("admin","moderator")` | admin / moderator |
| 🔑 | `OAuthClientBasicAuth` | 服务到服务 |

## 统计

- 本服务 GET 端点：**42**
  - 运维 1 · Galgame 核心 7 · 版本/PR/关系 8 · 消息 2 · 管理 4 · Tag 6 · Official 5 · Engine 4 · Series 5

---

## 0. 运维

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/health` | 🌐 | inline | ⏳ | 健康检查 |

## 1. Galgame 核心

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/galgame` | 🌐 | `galgameH.List` | ⏳ | 列表（status=0；含 released_from/to/months 筛选）|
| `GET /api/galgame/search` | 🔐 | `searchH.Galgame` | ⏳ | Meilisearch；默认 status=0；include_pending 看自己 pending |
| `GET /api/galgame/batch` | 🔐 | `galgameH.BatchGet` | ⏳ | 按 id 批量轻量 DTO（不含 release_date 等）|
| `GET /api/galgame/check` | 🌐 | `galgameH.CheckVNDB` | ⏳ | VNDB 重复校验 |
| `GET /api/galgame/user/:id/stats` | 🌐 | `galgameH.UserStats` | ⏳ | 用户贡献统计 |
| `GET /api/galgame/mine` | 🔒 | `submissionH.ListMine` | ⏳ | 我的投稿 |
| `GET /api/galgame/:gid` | 🔐 | `galgameH.Get` | ⏳ | 详情（嵌入 tag/official/engine 的 galgame_count、names 等）|

## 2. 版本历史 / PR / 关系子资源

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/galgame/:gid/revisions` | 🌐 | `revisionH.ListRevisions` | ⏳ | |
| `GET /api/galgame/:gid/revisions/:rev` | 🌐 | `revisionH.GetRevision` | ⏳ | |
| `GET /api/galgame/:gid/revisions/:rev/diff` | 🌐 | `revisionH.GetRevisionDiff` | ⏳ | 含 names 映射 |
| `GET /api/galgame/:gid/prs` | 🌐 | `revisionH.ListPRs` | ⏳ | |
| `GET /api/galgame/:gid/prs/:id` | 🌐 | `revisionH.GetPR` | ⏳ | 含 names 映射 |
| `GET /api/galgame/:gid/links` | 🌐 | `linkH.ListLinks` | ⏳ | |
| `GET /api/galgame/:gid/aliases` | 🌐 | `linkH.ListAliases` | ⏳ | |
| `GET /api/galgame/:gid/contributors` | 🌐 | `contributorH.List` | ⏳ | |

## 3. 消息（投稿事件流）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/galgame/messages/feed` | 🔑 | `messageH.ListFeed` | ⏳ | 下游 cron 拉取（含 target_user_id 的事件）|
| `GET /api/galgame/messages/mine` | 🔒 | `messageH.ListMine` | ⏳ | 我的通知 |

## 4. 管理（admin / moderator）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/admin/stats` | 🛡️ | `adminH.Stats` | ⏳ | 仪表盘统计 |
| `GET /api/admin/galgame` | 🛡️ | `adminH.ListGalgames` | ⏳ | 管理列表（任意 status）|
| `GET /api/admin/galgame/messages` | 🛡️ | `messageH.ListAdminQueue` | ⏳ | 审核队列 |
| `GET /api/admin/galgame/:gid` | 🛡️ | `adminH.GetGalgame` | ⏳ | 管理详情（任意 status）|

## 5. Tag

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/tag` | 🌐 | `tagH.List` | ⏳ | |
| `GET /api/tag/search` | 🌐 | `searchH.Tag` | ⏳ | Meilisearch |
| `GET /api/tag/multi` | 🌐 | `tagH.Multi` | ⏳ | 多 tag 交集（galgame-filter 页）|
| `GET /api/tag/:name` | 🌐 | `tagH.GetByName` | ⏳ | |
| `GET /api/tag/:id/revisions` | 🌐 | `taxRevH.TagListRevisions` | ⏳ | |
| `GET /api/tag/:id/revisions/:rev` | 🌐 | `taxRevH.TagGetRevision` | ⏳ | |

## 6. Official（会社）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/official` | 🌐 | `officialH.List` | ⏳ | |
| `GET /api/official/search` | 🌐 | `searchH.Official` | ⏳ | Meilisearch |
| `GET /api/official/:name` | 🌐 | `officialH.GetByName` | ⏳ | |
| `GET /api/official/:id/revisions` | 🌐 | `taxRevH.OfficialListRevisions` | ⏳ | |
| `GET /api/official/:id/revisions/:rev` | 🌐 | `taxRevH.OfficialGetRevision` | ⏳ | |

## 7. Engine（引擎）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/engine` | 🌐 | `engineH.List` | ⏳ | |
| `GET /api/engine/:name` | 🌐 | `engineH.GetByName` | ⏳ | |
| `GET /api/engine/:id/revisions` | 🌐 | `taxRevH.EngineListRevisions` | ⏳ | |
| `GET /api/engine/:id/revisions/:rev` | 🌐 | `taxRevH.EngineGetRevision` | ⏳ | |

## 8. Series（系列）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/series` | 🌐 | `seriesH.List` | ⏳ | |
| `GET /api/series/search` | 🌐 | `seriesH.Search` | ⏳ | |
| `GET /api/series/:id` | 🌐 | `seriesH.Get` | ⏳ | |
| `GET /api/series/:id/revisions` | 🌐 | `taxRevH.SeriesListRevisions` | ⏳ | |
| `GET /api/series/:id/revisions/:rev` | 🌐 | `taxRevH.SeriesGetRevision` | ⏳ | |
