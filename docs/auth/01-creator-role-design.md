# 创作者(creator)角色设计

> 2026-06-18 — OAuth/identity 层的「创作者」角色:可信发布者档位,赋予「galgame 直接发布
> (含无 VNDB ID)」等发布信任能力。授予走 **申请 → 管理员审核 → 通过/拒绝(可重申)**;
> 资格门槛由下游(论坛/补丁站)自治,中央申请队列 + 审核 + 角色授予在 OAuth。
>
> 关联:角色 claim 见 [integration/oauth](../integration/oauth/01-oauth-endpoints.md);发布信任
> 能力落在 wiki(galgame 服务);贡献数据复用 wiki 既有 `GET /galgame/user/:id/stats`。

## 1. 背景与现状

- **角色归 OAuth 独占**:`roles` 表(name 唯一)+ `user_roles` 多对多;用户角色是一个**集合**,经 `user.RoleNames()` 进 JWT 的 `roles` claim。下游 forum/moyu/wiki **只验签、读 roles,不发角色**。
- **现有角色**:`user` / `creator` / `moderator` / `admin`(seed 于 `cmd/migrate`)+ `ren`(莲,仅 DB 预置)。
- **现状缺口**:galgame 发布权二元 —— 普通用户只能「提交进审核队列」(`Submit` → `Pending`),只有 moderator/admin 能「直接发布」。`creator` 填补 user↔moderator 之间的「可信发布者」档。

## 2. 设计目标

1. 提供「可信发布者」中间档,补上 user↔moderator 空位。
2. 角色由 OAuth 拥有(集合、附加),下游零角色写入。
3. 授予经**人工审核**(高信任权限应人工把关),门槛客观、下游自治。
4. 最小权限:只给发布信任能力,不给审核/管理。
5. 最小实现:复用既有授予路径与既有 wiki 贡献统计。

## 3. 核心决策

### 决策 1:creator = OAuth 附加角色,显式 gate,不重排层级

角色是集合,`creator` 只是又一个字符串;能力用显式判定挂在 gate(`hasRole(roles, "creator", "moderator", "admin", "super_admin")`),不动 user/moderator/admin 的内部编号。

### 决策 2:授予 = 申请 → 管理员审核 → 通过/拒绝(可重申)

**不做自动晋升。** creator 的特权是「绕过审核直接发布 + 无 VNDB 发布」,属高信任权限 —— 业界对高信任权限走人工申请审核(Wikipedia「Requests for permissions」),而非按贡献数自动授予(那适合低风险档,如 Discourse 信任等级 / Wikipedia autoconfirmed)。人工审核也给运营**策展控制**,避免按含糊的贡献代理一次性放出大量直发者。

- 流程:申请 → 进中央队列 → 管理员**通过**(授予角色)或**拒绝**(附理由)。
- **可重申**:拒绝后过冷却期可再申请;冷却 **1 天**;同时只允许一个 pending(DB 偏唯一索引兜底)。

### 决策 3:三层归属 —— 资格判定下游自治(不是反模式)

资格门槛依赖**领域数据**,数据在哪、判定就在哪:

```
wiki   = galgame 贡献【数据】owner:已有 GET /galgame/user/:id/stats 暴露
         pr_merged(合并 PR 数)、galgame_created(已发布 galgame 数)等
论坛   = 简评(rating)【数据】owner;补丁站 = 补丁资源【数据】owner
论坛/补丁站(面向用户)= 【policy】owner:调 wiki stats 取 PR/galgame 数 + 查自己的
         简评/补丁数 + 套自己的「OR 阈值」;合格则 s2s 提交申请到 OAuth
OAuth  = 申请队列 + 管理员审核 + 角色授予(身份契约:只有 OAuth 能发角色)
```

- 让下游自己定门槛**不是反模式**,反而正确:资格依赖只有下游/wiki 拥有的数据,判定就该在数据所在地;反过来(把各站 policy 塞进 OAuth、让 OAuth 反查下游库)才是反模式。
- **角色仍单一**(一个 OAuth `creator`,全生态通用);碎片化的只是 policy,论坛改门槛只改下游、OAuth 不动。
- **安全边界**:下游把关「能不能申请」(软门槛),OAuth + 管理员把关「能不能真授予」(硬门槛)。下游门槛写松/有 bug 最坏只是塞满审核队列,绝不能绕过人工审核拿到角色。`evidence` 是下游随申请带上的「满足了哪条」提示,供管理员参考,不作授权依据。

### 决策 4:最小权限

creator 只拿:直接发布 galgame、无 VNDB 发布、(P2)更高上传配额、moyu `creator_only` 豁免、徽章。不碰审核队列、删他人内容、改既有分类。

## 4. 能力矩阵

图例:支持 / 不支持 / 不适用。

| 能力 | user | creator | moderator+ | 落点 |
|---|---|---|---|---|
| 提交 galgame(进审核队列) | 支持 | 不适用(可直发) | 不适用 | wiki `Submit` |
| 直接发布 galgame(status=0,绕过审核) | 不支持 | 支持 | 支持 | wiki `Submit`(角色分流) |
| 不填 VNDB ID 发布 | 仅提交时 | 支持 | 支持 | wiki(Submit 已放行空 vndb) |
| 编辑自己创建/参与的词条 | 支持 | 支持 | 支持 | wiki `Update`(现状即如此) |
| 上传配额 | 低 | 中(GB 级) | 高 | forum/moyu 上传处(P2) |
| moyu `creator_only` 模式下发布 | 不支持 | 支持(豁免) | 支持 | moyu `ensureCanPublishGalgame`(P2) |
| 创作者徽章 | 不支持 | 支持 | 不适用 | 前端(P2) |
| 审核队列 / 删他人内容 / 改既有分类 | 不支持 | 不支持 | 支持 | 不授予 creator |

> wiki 后端核心改动只有一处:`Submit` 依角色把新词条 status 分流为 Published(0)
> (creator/moderator/admin)或 Pending(3)(普通用户)。空 VNDB ID 已被 Submit 放行,
> 故「无 VNDB 直发」随分流自动成立。

## 5. 申请 → 审核流程

```
论坛: 用户点申请 → 论坛后端按【它的】门槛查:wiki /user/:id/stats(pr_merged / galgame_created)
        + 论坛自己的简评数 → 合格则带 user token + evidence 调 OAuth POST /api/v1/creator/applications
补丁站: 同理,门槛查 wiki pr_merged + moyu 自己的补丁资源数
OAuth: creator_applications 一张表(中央队列)
        管理员:GET /admin/creator/applications(默认 pending)
               POST .../:id/approve → AssignRole(creator,即 AddRole)+ 标记 approved
               POST .../:id/decline {reason} → 标记 declined(可重申,1 天冷却)
用户: GET /api/v1/creator/applications/me 查自己的申请状态
```

申请守卫(服务层):① 已是 creator → 拒;② 已有 pending → 拒;③ 上一条 declined 且距今 < 1 天 → 拒(冷却)。审核为状态机原子转移(并发双审 → 「已处理」)。

## 6. 资格门槛(下游定义,会变)

数据来源已标注 owner;阈值/口径由下游自治、随时可改(改下游代码,OAuth 不动):

- **论坛(kungal)**:`wiki.pr_merged ≥ 5` OR `wiki.galgame_created ≥ 10` OR `forum.简评(≥100字) ≥ 5`
- **补丁站(moyu)**:`moyu.补丁资源 ≥ 3` OR `wiki.pr_merged ≥ 5`

wiki 侧的 `pr_merged` / `galgame_created` 复用**既有** `GET /galgame/user/:id/stats`(`UserGalgameStats`),无需新端点。

## 7. 数据结构与端点

`creator_applications`(库 `kun_galgame_infra`):

```
id, user_id, source('forum'|'moyu'|…), status('pending'|'approved'|'declined'),
evidence jsonb(下游提供「满足了哪条」提示), message(申请人附言),
reviewer_id, reviewed_at, decline_reason, created_at, updated_at
偏唯一索引 uq_creator_app_pending: (user_id) WHERE status='pending'  -- 同时仅一个 pending
```

端点(OAuth,`/api/v1`):
- 用户(`middleware.Auth`):`POST /creator/applications`(申请,body: source/message/evidence)、`GET /creator/applications/me`(查状态)。
- 管理员(`RequireRole("admin")`):`GET /admin/creator/applications?status=`、`POST /admin/creator/applications/:id/approve`、`POST /admin/creator/applications/:id/decline`。

## 8. 落点清单(精确,按仓)

**infra / OAuth(`apps/api`)** — 已完成
- `creator` 角色 seed(`cmd/migrate`)+ `manageableRoles` + `callerCanManageRole`(admin/ren 可发撤)+ `admin_dto` oneof。
- `model.CreatorApplication` + `cmd/migrate`(AutoMigrate + 偏唯一索引)。
- 仓 `CreatorApplicationRepository` + 服务 `CreatorApplicationService`(申请守卫 / 审核 / approve→AddRole)+ 处理器 + 路由。
- 错误码 17001-17005。

**infra / wiki(galgame 服务)** — 已完成
- `Submit` 角色分流(creator/moderator/admin 直发);贡献统计复用**既有** `/user/:id/stats`(无新端点)。

**下游 forum / moyu(P2,各自仓)** — 未开始
- `RoleFromOAuthRoles` 认 `creator`;资格判定(调 wiki stats + 查自有数据)+ 申请入口 UI + 状态展示;上传配额档;moyu `creator_only` 豁免;前端徽章。

## 9. 迁移(schema 影响 → 必须提醒)

- **库 `kun_galgame_infra`**(非 wiki 库):`go run ./cmd/migrate` —— seed `creator` 角色 + 建 `creator_applications` 表 + 偏唯一索引。**部署不自动执行**(见 deploy-migration-gap)。
- 顺序:先 migrate + 部署认 `creator` 的代码,再开放申请入口。

## 10. 分阶段与状态

| 阶段 | 内容 | 状态 |
|---|---|---|
| P0 | OAuth `creator` 角色 + wiki `Submit` 直发 gate | 已完成(2026-06-18) |
| P1 | OAuth 申请队列 + 审核 + approve→授予(申请/状态/列表/通过/拒绝) | 已完成(2026-06-18) |
| P2 | 下游 forum/moyu:资格判定 + 申请入口 + 配额/creator_only/徽章 | 未开始(各自仓) |

> 历史:曾设计「按贡献数自动晋升 job」(grant-creator-role),已废弃删除,改为本文件的人工申请审核。

## 11. 取舍与安全(实事求是)

- **无 VNDB 直发滥用**:creator 直发词条可回滚、可 revert、角色可随时 revoke;人工审核是硬门槛。
- **下游门槛自治**:软门槛在下游,硬门槛在管理员;下游可信(一方服务),且无论如何绕不过审核。
- **生效延迟**:授予后经 token 刷新生效,符合现有 OAuth 模型。
- **服务层未加 DB 测试**:auth 包无身份库测试 harness(现有为 mock),逻辑直白,靠 build/vet + 部署校验;如需可后续引入接口 + mock。

## 12. 参考

- [Wikipedia: Requests for permissions — 高信任权限走人工申请审核](https://en.wikipedia.org/wiki/Wikipedia:Requests_for_permissions)
- [Wikipedia: User access levels — 低风险档自动授予的对照](https://en.wikipedia.org/wiki/Wikipedia:User_access_levels)
- [RBAC 最佳实践 — 角色集中于 IdP、最小权限(IBM)](https://www.ibm.com/think/topics/role-based-access-control-implementation)
