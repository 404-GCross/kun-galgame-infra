# Galgame Wiki 服务 — GET API 清单

> 服务: **galgame**（`apps/api/cmd/galgame`） · Base URL: `/api`
>
> 路由源: `apps/api/cmd/galgame/main.go`
>
> 配套: [oauth.get.md](./oauth.get.md) · [image.get.md](./image.get.md) · [moderation.get.md](./moderation.get.md) · [artifact.get.md](./artifact.get.md)
>
> **审计完成** —— 🔧 已修 / ✅ 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

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
| `GET /api/health` | 🌐 | inline | ✅ | 健康检查 |

## 1. Galgame 核心

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/galgame` | 🌐 | `galgameH.List` | 🔧 | 列表（status=0；含 released_from/to/months 筛选）；#19 limit 上限 50(防无界查询) |
| `GET /api/galgame/search` | 🔐 | `searchH.Galgame` | 🔧 | Meilisearch；默认 status=0；include_pending 看自己 pending；#04 非 admin/mod 的 status 仅允许 0，防 banned/pending/declined 泄露 |
| `GET /api/galgame/batch` | 🔐 | `galgameH.BatchGet` | ✅ | 按 id 批量轻量 DTO（不含 release_date 等）|
| `GET /api/galgame/check` | 🌐 | `galgameH.CheckVNDB` | ✅ | VNDB 重复校验 |
| `GET /api/galgame/user/:id/stats` | 🌐 | `galgameH.UserStats` | 🔧 | 用户贡献统计；#37 8 个 Count 错误传播，不再静默返回零 |
| `GET /api/galgame/user/:id/galgames` | 🌐 | `galgameH.UserGalgames` | ✅ | 用户已发布的 galgame 列表（下游个人主页「已发布」标签）；status=0 + content_limit 过滤（默认 sfw），total 为过滤后准确值 |
| `GET /api/galgame/mine` | 🔒 | `submissionH.ListMine` | ✅ | 我的投稿 |
| `GET /api/galgame/:gid` | 🔐 | `galgameH.Get` | 🔧 | 详情（嵌入 tag/official/engine 的 galgame_count、names 等）；#36 仅 published 才计 view，草稿不再被自增 |

## 2. 版本历史 / PR / 关系子资源

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/galgame/:gid/revisions` | 🌐 | `revisionH.ListRevisions` | 🔧 | #22 可见性门(隐藏条目 404，附 optionalJWT) |
| `GET /api/galgame/:gid/revisions/:rev` | 🌐 | `revisionH.GetRevision` | 🔧 | #22 可见性门 |
| `GET /api/galgame/:gid/revisions/:rev/diff` | 🌐 | `revisionH.GetRevisionDiff` | 🔧 | 含 names 映射；#22 可见性门 |
| `GET /api/galgame/:gid/prs` | 🌐 | `revisionH.ListPRs` | 🔧 | #22 可见性门 |
| `GET /api/galgame/:gid/prs/:id` | 🌐 | `revisionH.GetPR` | 🔧 | 含 names 映射；#22 可见性门；#40 gid 作用域校验；#07 返回 title/message |
| `GET /api/galgame/:gid/links` | 🌐 | `linkH.ListLinks` | ✅ | |
| `GET /api/galgame/:gid/aliases` | 🌐 | `linkH.ListAliases` | ✅ | |
| `GET /api/galgame/:gid/contributors` | 🌐 | `contributorH.List` | ✅ | |

## 3. 消息（投稿事件流）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/galgame/messages/feed` | 🔑 | `messageH.ListFeed` | 🔧 | 下游 cron 拉取（含 target_user_id 的事件）；#24 填充 effective_banner_hash(pinned cover) |
| `GET /api/galgame/revisions/recent` | 🔑 | `revisionH.RecentRevisions` | ✅ | 全站 merged 修订 feed（编辑动态）；下游 cron 镜像进本地时间线；since_id 游标 + has_more |
| `GET /api/galgame/messages/mine` | 🔒 | `messageH.ListMine` | 🔧 | 我的通知；#24 effective_banner_hash |

## 4. 管理（admin / moderator）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/admin/stats` | 🛡️ | `adminH.Stats` | 🔧 | 仪表盘统计；#43 Totals Count 错误传播；#44 daily 零填充连续 N 日序列 |
| `GET /api/admin/galgame` | 🛡️ | `adminH.ListGalgames` | 🔧 | 管理列表（任意 status）；#25 vndb_id 子串/前缀搜索(LIKE 通配+LOWER) |
| `GET /api/admin/galgame/messages` | 🛡️ | `messageH.ListAdminQueue` | 🔧 | 审核队列；#24 effective_banner_hash |
| `GET /api/admin/galgame/:gid` | 🛡️ | `adminH.GetGalgame` | ✅ | 管理详情（任意 status）|

## 5. Tag

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/tag` | 🌐 | `tagH.List` | 🔧 | #45 limit 上限 100 |
| `GET /api/tag/search` | 🌐 | `searchH.Tag` | 🔧 | Meilisearch；#05 前端按 {items,total} 信封解析(BE 契约不变) |
| `GET /api/tag/multi` | 🌐 | `tagH.Multi` | 🔧 | 多 tag 交集（galgame-filter 页）；#45 limit 上限 50 |
| `GET /api/tag/:name` | 🌐 | `tagH.GetByName` | 🔧 | #09 sort_field/order SQL 注入(白名单)；#26 galgame_count；#45 limit |
| `GET /api/tag/:id/revisions` | 🌐 | `taxRevH.TagListRevisions` | ✅ | |
| `GET /api/tag/:id/revisions/:rev` | 🌐 | `taxRevH.TagGetRevision` | ✅ | |

## 6. Official（会社）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/official` | 🌐 | `officialH.List` | 🔧 | #45 limit 上限 100 |
| `GET /api/official/search` | 🌐 | `searchH.Official` | 🔧 | Meilisearch；#06 前端按信封解析(BE 不变) |
| `GET /api/official/:name` | 🌐 | `officialH.GetByName` | 🔧 | #09 sort 注入白名单；#26 galgame_count；#45 limit |
| `GET /api/official/:id/revisions` | 🌐 | `taxRevH.OfficialListRevisions` | ✅ | |
| `GET /api/official/:id/revisions/:rev` | 🌐 | `taxRevH.OfficialGetRevision` | ✅ | |

## 7. Engine（引擎）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/engine` | 🌐 | `engineH.List` | ✅ | |
| `GET /api/engine/:name` | 🌐 | `engineH.GetByName` | 🔧 | #26 galgame_count；#45 limit 上限 50 |
| `GET /api/engine/:id/revisions` | 🌐 | `taxRevH.EngineListRevisions` | ✅ | |
| `GET /api/engine/:id/revisions/:rev` | 🌐 | `taxRevH.EngineGetRevision` | ✅ | |

## 8. Series（系列）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/series` | 🌐 | `seriesH.List` | ✅ | |
| `GET /api/series/search` | 🌐 | `seriesH.Search` | ✅ | |
| `GET /api/series/:id` | 🌐 | `seriesH.Get` | ✅ | |
| `GET /api/series/:id/revisions` | 🌐 | `taxRevH.SeriesListRevisions` | ✅ | |
| `GET /api/series/:id/revisions/:rev` | 🌐 | `taxRevH.SeriesGetRevision` | ✅ | |
