# KUN OAuth Admin - TODO 清单

> 基于全量代码审计生成，按优先级排列。

---

## 一、实现状态总览

### 后端模块

| 模块 | Model/DTO/Repo/Service | Handler | 总体完成度 |
|------|----------------------|---------|-----------|
| **Auth（认证）** | 全部完成 | 全部完成 | **100%** |
| **Site（站点）** | 全部完成 | Create/Update/CreateClient 是空 stub | **65%** |
| **Game（游戏）** | 全部完成 | 5 个 handler 是空 stub（Create/Update/Revision 等） | **60%** |
| **Content（内容）** | 全部完成 | Create/Update/Delete 是空 stub | **70%** |
| **Comment（评论）** | 全部完成 | List/Create/Update 是空 stub | **65%** |
| **Artifact（文件分发）** | 全部完成 | Create/Download 半成品；pipeline 三步全是 stub | **40%** |
| **Moderation（审核）** | 全部完成 | OpenAI/Anthropic provider 全是 stub（返回硬编码 approved） | **50%** |

### 基础设施

| 组件 | 状态 |
|------|------|
| PostgreSQL + GORM | ✅ 完成 |
| Redis Cache | ✅ 完成 |
| Mail（SMTP） | ✅ 完成 |
| **Queue（asynq）** | ❌ 完全未实现，stub 空壳 |
| **Storage（对象存储）** | ❌ 完全未实现，所有方法都是 stub |
| Worker（cmd/worker） | ❌ 空壳，asynq 代码全部注释掉 |

### 前端页面

| 页面 | 状态 |
|------|------|
| 登录/注册/忘记密码/重置密码 | ✅ 基本完成 |
| Dashboard（首页） | ⚠️ 数据全是硬编码假数据 |
| 用户管理 | ❌ `fetchUsers()` 被注释掉，页面永远显示"暂无用户" |
| 站点管理 | ⚠️ 列表可展示，创建/编辑按钮无功能 |
| OAuth 客户端 | ⚠️ 同上，创建按钮无功能 |
| 游戏管理 | ⚠️ 列表可展示，详情页 `/games/:id` 不存在 |
| 审核管理 | ⚠️ 列表可展示，审核详情页 `/moderation/:id` 不存在 |

---

## 二、BUG 和安全问题

### P0 — 严重（CRITICAL）

- [ ] **OAuth 2.0 端点完全缺失** — `OAuthService` 已写好但未注册到路由。`/oauth/authorize`、`/oauth/token`、`/oauth/userinfo`、`/oauth/revoke` 四个端点不存在。这是项目核心功能，目前完全不可用。
  - 文件：`internal/app/router.go`
  - 修复：为 OAuthService 创建 OAuthHandler，注册四个路由

- [ ] **角色（Role）从未被写入 JWT** — `GenerateTokens()` 生成 token 时 `Roles` 字段始终为空数组。后端 `RequireRole("admin")` 中间件永远拒绝所有人，admin API 全部不可用。
  - 文件：`pkg/utils/jwt.go`、`internal/platform/auth/service/auth_service.go`
  - 修复：在生成 token 时查询用户角色并写入 claims

- [x] **前端 admin 中间件不检查角色** — ~~已修复：`admin.ts` 现在检查 `isAdmin`~~

- [x] **前端 User 接口缺少 `role` 字段** — ~~已修复：User interface 增加了 `roles: string[]`~~

### P1 — 高危（HIGH）

- [ ] **Logout 不失效 JWT** — 删除了 Session 记录，但 JWT 本身还有 15 分钟有效期，无 token 黑名单机制。被盗 token 登出后仍可使用。
  - 文件：`internal/platform/auth/service/auth_service.go`
  - 修复：在 Redis 中维护 token 黑名单，auth 中间件检查黑名单

- [x] **前端 Cookie 无 `httpOnly` 标志** — ~~已修复：refresh_token 改为后端 Set-Cookie httpOnly cookie，access_token 保留短期 cookie（15 分钟）~~

- [ ] **Session 表存储了完整 JWT** — `SessionToken` 字段存的是 JWT 本身，而非独立的 session ID。JWT 泄露 = session 标识泄露。
  - 文件：`internal/platform/auth/service/auth_service.go` line 131
  - 修复：生成独立 session token（如 UUID），不用 JWT 本身

- [ ] **密码重置 token 先创建再发邮件** — 如果邮件发送失败，token 已入库但用户收不到邮件，产生孤儿 token。
  - 文件：`internal/platform/auth/service/auth_service.go`
  - 修复：使用事务，邮件失败时回滚 token 创建

- [ ] **登录端点无独立限流** — 使用全局 100 req/min，应该用更严格的限流防暴力破解。
  - 文件：`internal/app/router.go`
  - 修复：对 `/auth/login` 使用 StrictRateLimit

### P2 — 中等（MODERATE）

- [ ] **注册存在 TOCTOU 竞态** — 先查 email 是否存在再创建，并发请求可绕过唯一检查（DB 约束会兜底，但错误处理不友好）。
  - 修复：捕获 DB 唯一约束错误并转为友好提示

- [ ] **密码最低长度仅 6 位** — 偏弱，建议至少 8 位。
  - 文件：`internal/platform/auth/dto/auth_dto.go`

- [ ] **迁移用户无需旧密码即可改密** — `ChangePassword` 对 `password=NULL` 的用户跳过旧密码验证，存在安全窗口。
  - 文件：`internal/platform/auth/service/auth_service.go`

- [ ] **JWT Expires 配置项从未使用** — `.env` 中 `JWT_EXPIRES=90d` 被加载但代码里全是硬编码 15min/7day。
  - 文件：`pkg/config/config.go`、`pkg/utils/jwt.go`

- [ ] **CORS 硬编码了生产域名** — `kungal.com` 和 `moyu.moe` 写死在中间件里，不可配置。
  - 文件：`internal/middleware/cors.go`

- [ ] **无审计日志** — 所有关键操作（注册、登录、改密、封禁）无日志记录。

---

## 三、与 plans.md 的差距

| plans.md 设计 | 实际状态 | 优先级 |
|--------------|---------|--------|
| OAuth 2.0 + PKCE 完整流程 | Service 层写了，路由未注册 | P0 |
| asynq 任务队列 | 完全未实现 | P2 |
| 对象存储（临时/正式 bucket） | 完全未实现 | P2 |
| ClamAV 病毒扫描 | stub | P3 |
| AI 审核（OpenAI/Anthropic） | stub，返回硬编码 approved | P3 |
| 用户迁移脚本 | ✅ cmd/migrate-users 已实现 | — |
| DB 迁移 + Seed | ✅ cmd/migrate 已实现 | — |
| RBAC（Role + Permission） | Model 定义了，但未接入认证流程 | P0 |
| 管理后台完整功能 | 前端大量页面是空壳或假数据 | P1 |

---

## 四、未实现的 Handler Stub 清单

### Game Handler（5 个）
- [ ] `Create()` — 创建游戏
- [ ] `Update()` — 更新游戏
- [ ] `ListRevisions()` — 列出版本历史
- [ ] `CreateRevision()` — 创建新版本
- [ ] `Revert()` — 回滚版本

### Content Handler（3 个）
- [ ] `Create()` — 创建内容
- [ ] `Update()` — 更新内容
- [ ] `Delete()` — 删除内容

### Comment Handler（3 个）
- [ ] `List()` — 列出评论
- [ ] `Create()` — 创建评论
- [ ] `Update()` — 更新评论

### Site Handler（3 个）
- [ ] `Create()` — 创建站点
- [ ] `Update()` — 更新站点
- [ ] `CreateClient()` — 创建 OAuth 客户端

### Artifact Handler（2 个）
- [ ] `Create()` — 上传文件（需要 presigned URL）
- [ ] `Download()` — 下载文件（需要 presigned URL）

### Moderation Handler（1 个）
- [ ] `ListPolicies()` — 列出审核策略

---

## 五、未实现的基础设施

- [ ] **Queue（asynq）** — `internal/infrastructure/queue/queue.go` 全是空方法
- [ ] **Storage（对象存储）** — `internal/infrastructure/storage/storage.go` 全是空方法
- [ ] **Worker** — `cmd/worker/main.go` asynq 代码全部注释掉
- [ ] **Artifact Pipeline** — checksum/virus_scan/manifest_validator 三个 pipeline step 全是 stub

---

## 六、前端缺失功能

- [ ] Dashboard 统计数据接入真实 API
- [ ] 用户管理页面：取消注释 `fetchUsers()`，后端需要增加用户列表接口（目前只有 `/users/:uuid`）
- [ ] 站点管理页面：实现创建/编辑弹窗
- [ ] OAuth 客户端页面：实现创建弹窗
- [ ] 游戏详情页 `/games/:id`
- [ ] 审核详情页 `/moderation/:id`
- [ ] 站点详情/编辑页 `/sites/:id`

---

## 七、建议修复顺序

1. ✅ ~~注册 OAuth 路由 + 修复角色写入 JWT（P0）~~ — 已完成
2. ✅ ~~修复前端 admin 权限校验 + httpOnly cookie（P0）~~ — 已完成
3. 补全 Site/OAuthClient 的 Create handler（OAuth 流程前置）
4. 实现 token 黑名单（用 Redis）
5. 补全用户列表接口 + 前端用户管理页
6. 其余 handler stub 按需补全
7. 基础设施（Queue/Storage）按需补全
