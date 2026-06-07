# API 字段对齐审计

> 目的：逐服务记录全部 API 端点 + FE↔BE 字段对齐审计状态。
>
> 当前进度：**审计完成（2026-05-29）** —— 全部 147 端点（GET 75 / POST 42 / PUT 12 / DELETE 14 / PATCH 4）已逐项扫描。oauth / image / galgame 三服务**全部审计**：发现并修复 **46 项**问题（10 HIGH / 16 MEDIUM / 20 LOW，跨 51 个端点行标 🔧），其余标 ✅（本轮未发现可处理问题）。moderation / artifact **已有独立服务与路由**（`cmd/moderation` / `cmd/artifact`），本轮字段对齐审计未覆盖（状态 ⏳）——“未实现”的旧表述已不准确。详见下方 [审计结果](#审计结果2026-05-29)。
>
> 审计后新增（🆕）：`GET /api/v1/auth/me/moemoepoint/log` —— 用户自助查自己的萌萌点流水，供 oauth web `/profile`「萌萌点记录」使用。oauth GET 端点 19 → **20**，总计 147 → **148**。

## 端点矩阵（按服务 × 方法）

| 服务 | 二进制 | Base URL | GET | POST | PUT | DELETE | PATCH | 小计 |
|---|---|---|---|---|---|---|---|---|
| OAuth | `cmd/oauth` | `/api/v1` | [20](./oauth.get.md) | [21](./oauth.post.md) | [4](./oauth.put.md) | [3](./oauth.delete.md) | [2](./oauth.patch.md) | 50 |
| Image | `cmd/image`（+管理端在 oauth 进程）| `/`、`/api/v1/admin/image` | [6](./image.get.md) | [2](./image.post.md) | — | [2](./image.delete.md) | [1](./image.patch.md) | 11 |
| Galgame Wiki | `cmd/galgame` | `/api` | [42](./galgame.get.md) | [17](./galgame.post.md) | [8](./galgame.put.md) | [8](./galgame.delete.md) | [1](./galgame.patch.md) | 76 |
| Moderation | `cmd/moderation` | `/api/v1` | [4](./moderation.get.md) | [1](./moderation.post.md) | — | — | — | 5 |
| Artifact | `cmd/artifact` | `/api/v1` | [4](./artifact.get.md) | [1](./artifact.post.md) | — | [1](./artifact.delete.md) | — | 6 |
| **合计** | | | **76** | **42** | **12** | **14** | **4** | **148** |

> 注：`/api/v1/admin/image/*`、`/api/v1/admin/jobs/*` 物理上跑在 oauth 进程（admin 鉴权在那边）。image 管理端归到 image.* 审计；jobs 管理端归到 oauth.* 审计。上表 Image 的 DELETE/PATCH 各含 1 个 oauth 进程内的管理端端点。

## 审计结果（2026-05-29）

> 范围：逐个审计 oauth / image / galgame 三服务的全部端点（字段对齐 / 越权 / SQL 注入 / 静默失败 / 预期外副作用）。moderation / artifact 尚未实现（仅占位），按要求跳过。
>
> 方法：20 切片并行审计 + 逐项对抗式复核 → **46 项确认**（另有 5 项被复核驳回，见末尾）。全部已修复。

**验证图例**：🧪 有 DB 后端测试覆盖（新增/既有套件通过） · 🔬 go build + vet + 既有套件 + 代码审查（该服务无 HTTP/集成 harness） · 🌐 前端改动（类型校验 + 沿用既有 `Table.vue` 模式） · ⚠️ 本测试环境无法直接端到端验证（需 Meilisearch / 下游服务 / 邮件）

### HIGH（10）

| # | 端点 | 修复 | 验证 |
|---|---|---|---|
| 01 | `auth/me`·`/users/:uuid`·admin users | `UserResponse` 全投影补 `avatar_image_hash`（头像在 admin/自助页可见）| 🔬 + 🌐 |
| 02 | `GET /admin/users` | `sort_by` ORDER BY 注入：repo 列白名单 + `clause.OrderByColumn` + handler `utils.Validate` | 🔬 |
| 03 | `DELETE /image/:hash` | service 未注入 DB → `SoftDelete` nil-panic 500：`cmd/image` 传 `Options{DB}` + nil 守卫 | 🧪 |
| 04 | `GET /galgame/search` | 匿名传 `?status=1/3/4` 泄露封禁/待审/拒绝：非 admin/mod 仅允许 status=0 | ⚠️（需 Meilisearch）|
| 05 | `GET /tag/search` | 前端按 `{items,total}` 信封解析（BE 契约不变）| 🌐 |
| 06 | `GET /official/search` | 同 #05 | 🌐 |
| 07 | `POST /:gid/prs`·`GET /:gid/prs/:id` | PR title/message 被丢弃且永不显示：model/DTO 拆 `Note`→`Title`+`Message` | 🧪 |
| 08 | `POST/DELETE /:gid/links`·`/aliases` | IDOR：任意登录用户改任意 galgame → 加 owner/admin 门（兼修 #42 不存在 gid→404）| 🔬 |
| 09 | `GET /tag/:name`·`/official/:name` | `sort_field/order` SQL 注入：repo 白名单 | 🔬（galgame 套件通过）|
| 10 | `PUT /series/:id` | 缺角色校验：加 admin/moderator 门 | 🔬 |

### MEDIUM（16）

| # | 端点 | 修复 | 验证 |
|---|---|---|---|
| 11 | `POST /auth/refresh` | 封禁用户仍能续 token：加 `IsBanned` 拒发 + 撤销会话；`UpdateUser` 转封禁时同样撤销 | 🔬 |
| 12 | `GET /users/:uuid` | IDOR 泄露任意用户 email：改公开投影（去 email/status）| 🔬 |
| 13 | `GET /oauth/userinfo` | `updated_at` 永不返回：设 `user.UpdatedAt.Unix()` | 🔬 |
| 14 | `GET /oauth/userinfo` | `picture` 忽略 `avatar_image_hash`：按 hash 解析 CDN URL | 🔬 |
| 15 | admin users 列表/详情 | DTO 漏 `avatar_image_hash`（同 #01 一并修）| 🔬 |
| 16 | `DELETE /sites/:id` | 有客户端时 FK 500：预检返回可读 400 | 🔬 |
| 17 | `POST /image/upload` | 重传软删 hash → UNIQUE 500：复活软删行（resurrect）| 🧪 |
| 18 | `PATCH /admin/image/:hash/review` | 人工理由覆盖自动审核标签：改 `jsonb_set` 合并 | 🔬 |
| 19 | `GET /galgame` | `limit` 无上限（DoS）：钳到 50 | 🔬 |
| 20 | `POST /galgame`·`PUT /:gid` | covers/screenshots 元素校验被跳过：加 `dive`+hex；Update 补 `Validate` | 🔬 |
| 21 | `PUT /galgame/:gid` | vndb_id 无格式/唯一校验（坏值入库 / 重复 500）：加校验 | 🧪 |
| 22 | `GET /:gid/revisions[...]`·`/prs[...]` | 泄露隐藏条目快照：加可见性门（+optionalJWT）| 🔬（新增 GetPR 测试）|
| 23 | `PATCH /galgame/:gid` | PatchDraft vndb_id 同 #21：加校验 | 🔬 |
| 24 | `messages/feed`·`/mine`·admin queue | `effective_banner_hash` 恒 null（缩略图空白）：批量填 pinned cover | 🔬 |
| 25 | `GET /admin/galgame` | vndb_id 搜索缺 `%` 通配（退化为精确匹配）：LIKE 通配+LOWER | 🔬 |
| 26 | `GET /tag/:name`·`/official/:name`·`/engine/:name` | 详情 `galgame_count` 恒 0：`FindByID` 补 cnt JOIN | 🔬 |

### LOW（20）

| # | 端点 | 修复 | 验证 |
|---|---|---|---|
| 27 | `POST /auth/password/reset` | 基础设施错误伪装成 400「验证码无效」：改 500 | 🔬 |
| 28 | `POST /auth/password/reset` | 非原子（重放窗口）：先原子单用消费 token，再改密 | 🔬 |
| 29 | `GET /users/:uuid` | roles 恒空：改 `GetCurrentUserWithRoles`（随 #12）| 🔬 |
| 30 | `GET /users/search` | `q` 按字节计长 → 满长 CJK 名误拒：改按 rune 计 | 🔬 |
| 31 | `PATCH /admin/users/:uuid` | not-found 返回 400：改 404（保留名/邮箱冲突 400）| 🔬 |
| 32 | `POST/PUT /oauth/clients[/:id]` | grants 空/任意值不校验（造死客户端）：`min=1,dive,oneof` | 🔬 |
| 33 | `POST /image/upload` | 并发同 hash 去重 500：`Create` OnConflict + 收敛到赢家 | 🧪 |
| 34 | `GET /admin/image/list` | 非法 from/to/review_status 静默全量：返回 400 | 🔬 |
| 35 | `GET /admin/image/stats` | by_site/unique 含软删与 total_bytes 口径不一致：统一排除软删 | 🔬 |
| 36 | `GET /galgame/:gid` | 草稿被自增 view：仅 published 计 view | 🧪（既有套件）|
| 37 | `GET /galgame/user/:id/stats` | 8 个 Count 吞错返回伪零：错误传播 | 🧪 |
| 38 | `PUT /galgame/:gid` | 无 status 门，绕过审核消息：草稿(3/4)非 admin 走 PATCH | 🧪 |
| 39 | `PUT /:gid/prs/:id/merge` | `completed_time` 用陈旧 `galgame.Updated`：改 `NOW()` | 🔬 |
| 40 | `GET/PUT /:gid/prs/:id[...]` | `:gid` 被忽略（跨 galgame 取 PR）：加作用域校验 | 🧪 |
| 41 | `PUT /:gid/prs/:id/merge`·diff | 快照切片非规范排序 → 伪 diff/伪冲突：相等比较改顺序无关 | 🔬 |
| 42 | `POST /:gid/links`·`/aliases` | 不存在 gid 返回 500：随 #08 预检 → 404 | 🔬 |
| 43 | `GET /admin/stats` | 7 个 Totals Count 吞错：错误传播 | 🧪 |
| 44 | `GET /admin/stats` | daily 缺无活动日期（图表断裂）：零填充连续 N 日 | 🧪 |
| 45 | tag/official/engine list/detail | `limit` 上限失效（DoS）：handler 钳上限 | 🔬 |
| 46 | `POST /series` | 同名创建 500：加 `ExistsByName` 预检 → 400 | 🔬 |

### 复核驳回的 5 项误报（保持 ✅，无需改动）

UpdateProfile avatar hash 已处理 · DELETE 命中缺失返回 200（幂等，by-design）· reference-ping 跨站（设计如此）· mapSubmissionError 400-vs-409（语义可接受）· 每日配额时区（已正确）。

### 本轮新建的测试基建 & 测试覆盖

- **MinIO**（docker `kun-test-minio` @ `127.0.0.1:9000`）+ Postgres 测试库 `kun_images_test`、`kun_galgame_wiki_test`（密码 191007，测试环境授权）。
- 新增 DB 后端测试：`image` 包 `TestHTTP_SoftDelete_ThenResurrectOnReupload` / `TestHTTP_SoftDelete_OtherSite_404`（#03/#17/#33）；`galgame/service` 包 `TestUpdate_RejectsInvalidVNDB` / `TestUpdate_RejectsDuplicateVNDB` / `TestUpdate_SameVNDBIsAllowed` / `TestUpdate_RejectsDraftForNonAdmin`（#21/#38）/ `TestGetPR_ScopedToGalgame_AndCarriesTitleMessage`（#07/#40）；`TestAdminStats_EmptyDatabase` 改零填充断言（#44）。
- 全量 `go build ./... && go vet ./... && go test -p 1 ./...` 全绿（`-p 1` 串行，规避 jobs/galgame 共库并发 AutoMigrate 的 `pg_class` 竞争——属测试 harness 限制，非代码缺陷）。

### 无法在本环境直接端到端验证（已标注）

- **#04**（galgame/search status 泄露修复）依赖 Meilisearch，本测试环境未起 Meilisearch；逻辑经代码审查 + 角色判断与既有 `include_pending` 路径一致。
- **oauth 服务**所有端点（#01/#02/#11–#16/#27–#32 等）：该服务无 HTTP/集成测试 harness（service 单测用 mock 且未注入），经 build+vet+既有单测+审查验证；如需端到端建议补一套 oauth HTTP harness。
- **image admin 端点**（#18/#34/#35）跑在 oauth 进程、无对应 harness：经 build+审查。
- **前端**（#05/#06/#01-fe）：经类型校验 + 沿用 `users/Table.vue` 既有解析/解析头像模式；未跑 Nuxt 端到端。

### 后续/迁移备注

- **#07** PR 模型由单列 `note` 拆为 `title`+`message`（AutoMigrate 自动加列，旧 `note` 列保留）。生产若有历史 PR 数据，回填一次：`UPDATE galgame_pr SET message = note WHERE message = '' AND note <> '';`（dev/test 库当前 0 行 PR，无需回填）。
- **#44** 仅做了「日期零填充」；严格的「Go since 边界 ↔ PG 会话时区对齐」需把 PG TimeZone 配置注入 repo，本轮未做（属部署期 TZ 一致性问题，已在 finding 标注为 deferred）。

## 共用图例

**审计状态**：✅ 对齐无问题 · 🔧 已修 · ⏭️ 有意保持 · ⏳ 待审计 · 🆕 本轮新增端点

**鉴权**：🌐 公开 · 🔐 OptionalJWT · 🔒 登录必需 · 🛡️ admin/moderator · ⚙️ admin · 🔑 OAuth Client Basic Auth（服务到服务）

> 鉴权细节差异：oauth 的 `Auth` 每次查 DB 用户状态（封禁/匿名化即拒）；galgame/moderation/artifact 的 `JWTAuth` 仅验签。各文件图例有标注。
