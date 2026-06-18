# 创作者(creator)角色重新引入设计

> 2026-06-18 — 在 OAuth/identity 层重新引入「创作者」角色:作为可信发布者档位,
> 自动晋升 + 管理员兜底授予,赋予「galgame 直接发布(含无 VNDB ID)」等发布信任能力。
> 优雅、最小、不过度设计:零新表、无人工审核队列、无自动降级。
>
> 关联:契约侧角色 claim 见 [integration/oauth](../integration/oauth/01-oauth-endpoints.md);
> 发布信任能力落在 wiki(galgame 服务)与下游 forum/moyu。

## 1. 背景与现状

- **角色归 OAuth/infra 所有**:`roles` 表(`name` 唯一)+ `user_roles` 多对多;用户角色是一个**集合**,经 `user.RoleNames() []string` 进 JWT 的 `roles` claim。下游 forum/moyu/wiki **只验签、读 `roles`,不发角色**(身份契约)。
- **现有角色**:`user` / `moderator` / `admin`(seed 于 `cmd/migrate/main.go`)+ `super_admin`(仅 DB 预置、API 不可发)。**没有 `creator`。**
- **授予机制**:管理员经 `POST /admin/users/:uuid/roles` 调 `AdminService.AssignRole` → `userRepo.AddRole(userID, roleName)`。**没有任何自助申请入口。**
- **legacy 对照**:creator 仅存在于 moyu(补丁站,role=2 publisher),论坛(kungal)从无此概念。moyu 的创作者特权全是发布类(无 VNDB 直发、5GB 配额、creator_only 豁免);该档在 OAuth 迁移时被并入 moderator 而消失。
- **现关键缺口**:galgame 发布权现在是二元的 —— 普通用户只能「提交进审核队列」(`Submit` → `status=Pending`),只有 moderator/admin 能「直接发布」(`Create`,且强制 VNDB ID)。`user` 与 `moderator` 之间没有「可信发布者」档。

## 2. 设计目标

1. 重新提供「可信发布者」中间档,补上 user↔moderator 的空位。
2. 角色继续由 OAuth 拥有(集合式、附加),下游零角色写入。
3. 授予低运维:优先客观标准**自动晋升**,管理员可手动兜底/撤销。
4. 能力遵循最小权限:只给发布信任相关能力,不给审核/管理。
5. 最小实现:尽量零新表、复用既有授予/发布/任务框架。

## 3. 核心决策(对齐业界最佳实践)

### 决策 1:creator = OAuth 附加角色,显式 gate,不重排层级

角色是集合,`creator` 只是又一个角色字符串。能力用**显式角色判定**挂在具体 gate(如 `hasRole(roles, "creator", "moderator", "admin", "super_admin")`),**不动** user/moderator/admin 的内部编号或继承关系。理由:creator 不是「moderator 减若干」,而是「user 加发布信任」,显式枚举比层级 ≥ 比较更贴合最小权限。

### 决策 2:授予 = 自动晋升(主)+ 管理员手动(兜底);不做申请审核队列

成熟社区平台对「可信贡献者」档位普遍采用**达到客观标准即自动授予 + 管理员可手动调整**,而非人工申请—审核流水线:

- Discourse Trust Levels:按参与度自动升降级(TL3 有客观标准、可降级)。
- Wikipedia autoconfirmed / extended-confirmed:账号龄 + 编辑数达标即自动授予,管理员可手动确认。

据此**砍掉 legacy 的 apply→review→approve/decline 整条流水线**(它是 legacy 最重的部分,且带来人情/积压)。creator 改为「达标即得」:

- 主路径:自动晋升 job(§5.1)。
- 兜底:管理员手动 grant/revoke(§5.2)。
- 可选:只读「进度」端点(§5.3)代替「申请」——用户看到的是进度,不是申请表单。

### 决策 3:零新表

自动晋升只依赖**现有** `user_roles`(已授予集合)+ **现有** wiki 贡献数据(`galgame_contributor` × `galgame.status`)。**不需要 application 表**。唯一 schema 动作是给 `roles` 表插入一行 `creator`(§8)。

### 决策 4:不自动降级

Discourse 会按标准降级;但对「发布信任」而言自动降级属过度设计。采用:自动晋升后**粘性保留**,滥用由管理员 `revoke`。

### 决策 5:最小权限

creator 只拿「可信发布」相关能力(直接发布、无 VNDB 发布、更高配额、creator_only 豁免、徽章),**不碰**审核队列、删他人内容、改既有分类等管理能力。

## 4. 能力矩阵

图例:支持 / 不支持 / 不适用。

| 能力 | user | creator | moderator+ | 落点 |
|---|---|---|---|---|
| 提交 galgame(进审核队列) | 支持 | 不适用(可直发) | 不适用 | wiki `Submit` |
| 直接发布 galgame(`status=0`,绕过审核) | 不支持 | 支持 | 支持 | wiki `Submit`(角色分流) |
| 不填 VNDB ID 发布(同人/独立) | 仅提交时 | 支持 | 支持 | wiki(`Submit` 已放行空 vndb) |
| 编辑自己创建/参与的词条 | 支持 | 支持 | 支持 | wiki `Update`(现状即如此) |
| 上传配额 | 低 | 中(GB 级) | 高 | forum/moyu 上传处(P2) |
| moyu `creator_only` 模式下发布 | 不支持 | 支持(豁免) | 支持 | moyu `ensureCanPublishGalgame`(P2) |
| 创作者徽章 | 不支持 | 支持 | 不适用 | 前端(P2) |
| 审核队列 / 删他人内容 / 改既有分类 | 不支持 | 不支持 | 支持 | 不授予 creator |

> 后端核心改动其实只有一处逻辑:wiki `Submit` 依据角色把新词条 `status` 分流为
> `Published(0)`(creator/moderator/admin)或 `Pending(3)`(普通用户)。空 VNDB ID 早已被
> `Submit` 放行,所以「无 VNDB 直发」随分流自动成立。复用 `Submit`,不新增端点。

## 5. 授予机制

### 5.1 自动晋升 job(主路径)

- 新 job `grant-creator-role`,接入现有 jobs 框架(`internal/jobs/all.go` 注册,每日定时,与其它 job 错峰)。
- 它在 infra 内**同时读 `kun_galgame_wiki`(贡献数)+ 写 `kun_galgame_infra`(角色)**——galgame 服务与 jobs 已具备双库连接(`oauthDB`=identity 库、`wiki`=wiki 库),无需服务间 HTTP。
- **资格**:`已审核通过的贡献数 ≥ N`(以 `galgame_contributor` 关联 `galgame.status=0` 的去重计数)**且账号正常**(`user.status` 非封禁)。`N` 默认 3(对齐 legacy「≥3 已发布」),配置化。
- **动作**:对达标且尚无 `creator` 的用户,幂等授予(`INSERT INTO user_roles SELECT user_id, (creator role id) ... ON CONFLICT DO NOTHING`,即复用 `AddRole` 语义)。
- **生效时机**:用户下次 token 刷新(forum/moyu/wiki 在刷新时重新读 `roles`),非即时——符合现有 OAuth 模型,可接受。

### 5.2 管理员手动 grant/revoke(兜底)

- `manageableRoles` 加 `"creator"`;`callerCanManageRole` 矩阵:`super_admin` 与 `admin` 均可发/撤 `creator`(creator 低于 moderator,无需特别提权)。
- `admin_dto` 的 `Role` 校验 `oneof` 加 `creator`。
- **复用现有** `POST /admin/users/:uuid/roles`,零新端点。

### 5.3 只读「创作者进度」端点(可选)

- `GET /creator/status` 返回 `{已审核通过贡献数, 阈值 N, 是否已是 creator}`,供前端展示进度条。纯读、无审核。**不做亦可**——自动晋升不依赖它。

### 5.4 明确不做

- 不做 apply→review→approve/decline 审核队列(决策 2)。
- 不做自动降级(决策 4)。

## 6. 关键实现细节(必读)

1. **`creator` 角色行必须先 seed。** `AddRole` 是 `INSERT INTO user_roles SELECT id FROM roles WHERE name=? ON CONFLICT DO NOTHING` —— 若 `roles` 表无 `creator` 行,SELECT 为空 → **授予静默失败**。因此 seed `creator` 行是先决步骤(§8)。
2. **`Submit` 分流。** `submission_handler.Submit` 从 `c.Locals`/中间件取调用方 `roles` 传入 `submissionSvc.Submit`;服务内 `publishDirect := hasRole(roles, "creator","moderator","admin","super_admin")`,据此把状态设为 `Published(0)` 或 `Pending(3)`。**直发也必须走既有 `ApplySnapshot` 单一写入路径**(§01 不变量),并补 contributor 与 revision(`action="created"`),与现有 `Submit` 流程一致,仅状态不同。
3. **无 VNDB 直发。** `Submit` 现已允许空 `vndb_id`(`submission_service.go` 只在非空时校验格式),分流后 creator 的空 vndb 词条直接 `status=0`,无需额外改动。
4. **资格口径。** 用「该用户为 `galgame_contributor` 且 `galgame.status=0`」的去重计数为准(P1 可调整为 merged revision 计数)。账号正常以 `user.status` 为准。
5. **能力判定单点化。** 新增一个语义清晰的判定(如 `auth` 侧 `CanPublishGalgameDirectly(roles)` 或 wiki 侧复用 `hasRole`),集中维护「哪些角色算可信发布者」,避免散落。

## 7. 落点清单(精确,按仓)

**infra / OAuth(`apps/api`)**
- `cmd/migrate/main.go`:角色 seed 列表加 `{Name:"creator", Description:"Trusted creator (direct galgame publish)"}`。
- `internal/platform/auth/handler/admin_handler.go`:`manageableRoles` 与 `callerCanManageRole` 矩阵加 `creator`。
- `internal/platform/auth/dto/admin_dto.go`:`Role` 的 `oneof` 加 `creator`。
- `internal/jobs/grant_creator_role.go`(新)+ 在 `internal/jobs/all.go` 注册(每日,错峰)。
- (可选)`GET /creator/status` 端点 + handler。

**infra / wiki(galgame 服务,`apps/api`)**
- `internal/platform/galgame/handler/submission_handler.go`:取 `roles` 传入 Submit。
- `internal/platform/galgame/service/submission_service.go`:`Submit` 增 `roles []string` 形参 + `publishDirect` 分流 + 直发簿记。

**下游 forum / moyu(P2,各自仓)**
- `RoleFromOAuthRoles` 认 `creator`(内部档或能力标签)。
- 上传配额表加 creator 档;moyu `ensureCanPublishGalgame` 放行 creator;前端创作者徽章。

## 8. 数据与迁移

- **唯一 schema 动作**:`roles` 表插入一行 `creator`,经 `cmd/migrate` 的角色 seed 完成。**无新表、无新列。**
- **执行**:`go run ./cmd/migrate`,作用于**主库 `kun_galgame_infra`**(非 wiki 库)。**部署不自动执行**(见 [deploy-migration-gap] 教训)。
- **顺序**:先 `cmd/migrate`(建 `creator` 行)+ 部署认 `creator` 的代码,**再**开自动晋升 job —— 否则 job 的授予会因角色行缺失而静默失败。

## 9. 分阶段实施与状态

| 阶段 | 内容 | 状态 |
|---|---|---|
| P0 | OAuth 加 `creator`(seed + manageableRoles + 矩阵 + dto)+ wiki `Submit` 分流 gate | 已完成(2026-06-18) |
| P1 | 自动晋升 job `grant-creator-role`(每日 05:15) | 已完成(2026-06-18);`/creator/status` 仍为可选、未做 |
| P2 | 下游 forum/moyu:配额档 / creator_only 豁免 / 徽章 | 未开始(各自仓) |

P0 完成即可上线:管理员可手动发 `creator`,creator 可无 VNDB 直发 galgame。

## 10. 取舍与安全(实事求是)

- **无 VNDB 直发滥用**:creator 直发词条仍**可回滚、可被 revert、角色可随时 revoke**;若担心,P1 可加「creator 直发」轻量 feed 供管理员抽查(非阻塞),远轻于「逐条人工审核」。
- **不自动降级**:简单优先;滥用即 admin revoke。
- **生效延迟**:角色经 token 刷新生效,非即时,符合现有 OAuth 模型。
- **资格阈值/口径可配**:`N` 默认 3;贡献计数口径 P1 可调。
- **能力扩散**:能力判定单点化(§6.5),避免「哪些角色算 creator」散落多处导致漂移。

## 11. 参考

- [Discourse Trust Levels — 按客观标准自动升降级 + 管理员可调](https://blog.discourse.org/2018/06/understanding-discourse-trust-levels/)
- [Wikipedia: User access levels — autoconfirmed/extended-confirmed 自动授予 + 管理员可手动确认](https://en.wikipedia.org/wiki/Wikipedia:User_access_levels)
- [RBAC 最佳实践 — 角色集中于 IdP、最小权限(IBM)](https://www.ibm.com/think/topics/role-based-access-control-implementation)
