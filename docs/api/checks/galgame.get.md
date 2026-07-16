# Galgame Wiki 服务 — GET API 清单

> 服务: **galgame 面**（宿主 `cmd/catalog`,装配 `apps/api/internal/galgameapp`;独立 `galgame` 二进制已于 wiki 退役 W5 移除） · Base URL: `/api`
>
> 路由源: `apps/api/internal/galgameapp/mount.go`
>
> 配套: [oauth.get.md](./oauth.get.md) · [image.get.md](./image.get.md) · [moderation.get.md](./moderation.get.md) · [artifact.get.md](./artifact.get.md)
>
> **审计完成** —— 已修 / 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 图例 — 审计状态

- 已审计无问题 · 已修 · 保持（有意保持当前行为） · 待审计

## 图例 — 鉴权

| 标记 | 中间件 | 含义 |
|---|---|---|
| 公开 | （无） | 完全公开 |
| OptionalJWT | `OptionalJWT` | 可选；带 token 附加自己的 pending/隐藏内容 |
| 登录 | `JWTAuth` | 必须登录。**仅验签**（不查 DB 状态；与 oauth 的 `Auth` 不同）|
| admin/mod | `JWTAuth` + `RequireRole("admin","moderator")` | admin / moderator |
| ClientAuth | `OAuthClientBasicAuth` | 服务到服务 |

## 统计

- 本服务 GET 端点：**42**
  - 运维 1 · Galgame 核心 7 · 版本/PR/关系 8 · 消息 2 · 管理 4 · Tag 6 · Official 5 · Engine 4 · Series 5

---

## 0. 运维

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/health` | 公开 | inline | 已审计 | 健康检查 |

## 1. Galgame 核心

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/galgame` | 公开 | `galgameH.List` | 已修 | 列表（status=0；含 released_from/to/months 筛选）；#19 limit 上限 50(防无界查询) |
| `GET /api/galgame/search` | OptionalJWT | `searchH.Galgame` | 已修 | Meilisearch；默认 status=0；include_pending 看自己 pending；#04 非 admin/mod 的 status 仅允许 0，防 banned/pending/declined 泄露 |
| `GET /api/galgame/batch` | OptionalJWT | `galgameH.BatchGet` | 已审计 | 按 id 批量轻量 DTO（不含 release_date 等）|
| `GET /api/galgame/check` | 公开 | `galgameH.CheckVNDB` | 已审计 | VNDB 重复校验 |
| `GET /api/galgame/user/:id/stats` | 公开 | `galgameH.UserStats` | 已修 | 用户贡献统计；#37 8 个 Count 错误传播，不再静默返回零 |
| `GET /api/galgame/user/:id/galgames` | 公开 | `galgameH.UserGalgames` | 已审计 | 用户已发布的 galgame 列表（下游个人主页「已发布」标签）；status=0 + content_limit 过滤（默认 sfw），total 为过滤后准确值 |
| `GET /api/galgame/mine` | 登录 | `submissionH.ListMine` | 已审计 | 我的投稿 |
| `GET /api/galgame/:gid` | OptionalJWT | `galgameH.Get` | 已修 | 详情（嵌入 tag/official/engine 的 galgame_count、names 等）；#36 仅 published 才计 view，草稿不再被自增 |

## 2. 版本历史 / PR / 关系子资源

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/galgame/:gid/revisions` | 公开 | `revisionH.ListRevisions` | 已修 | #22 可见性门(隐藏条目 404，附 optionalJWT) |
| `GET /api/galgame/:gid/revisions/:rev` | 公开 | `revisionH.GetRevision` | 已修 | #22 可见性门 |
| `GET /api/galgame/:gid/revisions/:rev/diff` | 公开 | `revisionH.GetRevisionDiff` | 已修 | 含 names 映射；#22 可见性门 |
| `GET /api/galgame/:gid/prs` | 公开 | `revisionH.ListPRs` | 已修 | #22 可见性门 |
| `GET /api/galgame/:gid/prs/:id` | 公开 | `revisionH.GetPR` | 已修 | 含 names 映射；#22 可见性门；#40 gid 作用域校验；#07 返回 title/message |
| `GET /api/galgame/:gid/links` | 公开 | `linkH.ListLinks` | 已审计 | |
| `GET /api/galgame/:gid/aliases` | 公开 | `linkH.ListAliases` | 已审计 | |
| `GET /api/galgame/:gid/contributors` | 公开 | `contributorH.List` | 已审计 | |

## 3. 消息（投稿事件流）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/galgame/messages/feed` | ClientAuth | `messageH.ListFeed` | 已修 | 下游 cron 拉取（含 target_user_id 的事件）；#24 填充 effective_banner_hash(pinned cover) |
| `GET /api/galgame/revisions/recent` | ClientAuth | `revisionH.RecentRevisions` | 已审计 | 全站 merged 修订 feed（编辑动态）；下游 cron 镜像进本地时间线；since_id 游标 + has_more |
| `GET /api/galgame/messages/mine` | 登录 | `messageH.ListMine` | 已修 | 我的通知；#24 effective_banner_hash |

## 4. 管理（admin / moderator）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/admin/stats` | admin/mod | `adminH.Stats` | 已修 | 仪表盘统计；#43 Totals Count 错误传播；#44 daily 零填充连续 N 日序列 |
| `GET /api/admin/galgame` | admin/mod | `adminH.ListGalgames` | 已修 | 管理列表（任意 status）；#25 vndb_id 子串/前缀搜索(LIKE 通配+LOWER) |
| `GET /api/admin/galgame/messages` | admin/mod | `messageH.ListAdminQueue` | 已修 | 审核队列；#24 effective_banner_hash |
| `GET /api/admin/galgame/:gid` | admin/mod | `adminH.GetGalgame` | 已审计 | 管理详情（任意 status）|

## 5. Tag

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/tag` | 公开 | `tagH.List` | 已修 | #45 limit 上限 100 |
| `GET /api/tag/search` | 公开 | `searchH.Tag` | 已修 | Meilisearch；#05 前端按 {items,total} 信封解析(BE 契约不变) |
| `GET /api/tag/multi` | 公开 | `tagH.Multi` | 已修 | 多 tag 交集（galgame-filter 页）；#45 limit 上限 50 |
| `GET /api/tag/:name` | 公开 | `tagH.GetByName` | 已修 | #09 sort_field/order SQL 注入(白名单)；#26 galgame_count；#45 limit |
| `GET /api/tag/:id/revisions` | 公开 | `taxRevH.TagListRevisions` | 已审计 | |
| `GET /api/tag/:id/revisions/:rev` | 公开 | `taxRevH.TagGetRevision` | 已审计 | |

## 6. Official（会社）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/official` | 公开 | `officialH.List` | 已修 | #45 limit 上限 100 |
| `GET /api/official/search` | 公开 | `searchH.Official` | 已修 | Meilisearch；#06 前端按信封解析(BE 不变) |
| `GET /api/official/:name` | 公开 | `officialH.GetByName` | 已修 | #09 sort 注入白名单；#26 galgame_count；#45 limit |
| `GET /api/official/:id/revisions` | 公开 | `taxRevH.OfficialListRevisions` | 已审计 | |
| `GET /api/official/:id/revisions/:rev` | 公开 | `taxRevH.OfficialGetRevision` | 已审计 | |

## 7. Engine（引擎）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/engine` | 公开 | `engineH.List` | 已审计 | |
| `GET /api/engine/:name` | 公开 | `engineH.GetByName` | 已修 | #26 galgame_count；#45 limit 上限 50 |
| `GET /api/engine/:id/revisions` | 公开 | `taxRevH.EngineListRevisions` | 已审计 | |
| `GET /api/engine/:id/revisions/:rev` | 公开 | `taxRevH.EngineGetRevision` | 已审计 | |

## 8. Series（系列）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/series` | 公开 | `seriesH.List` | 已审计 | |
| `GET /api/series/search` | 公开 | `seriesH.Search` | 已审计 | |
| `GET /api/series/:id` | 公开 | `seriesH.Get` | 已审计 | |
| `GET /api/series/:id/revisions` | 公开 | `taxRevH.SeriesListRevisions` | 已审计 | |
| `GET /api/series/:id/revisions/:rev` | 公开 | `taxRevH.SeriesGetRevision` | 已审计 | |
