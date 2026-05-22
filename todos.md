# 鲲 Galgame OAuth Admin - TODO 清单

> 基于全量代码审计生成，按优先级排列。
> Game/Content/Comment 模块已移除（站点业务数据由各网站自行管理）。

---

## 一、实现状态总览

### 后端模块

| 模块 | Model/DTO/Repo/Service | Handler | 总体完成度 |
|------|----------------------|---------|-----------|
| **Auth（认证）** | 全部完成 | 全部完成 | **100%** |
| **Site（站点）** | 全部完成 | Create/Update/CreateClient 是空 stub | **65%** |
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
| 审核管理 | ⚠️ 列表可展示，审核详情页 `/moderation/:id` 不存在 |

---

## 二、BUG 和安全问题

### P0 — 严重（CRITICAL）

- [x] **OAuth 2.0 端点完全缺失** — ~~已修复：创建 OAuthHandler，注册四个路由~~
- [x] **角色（Role）从未被写入 JWT** — ~~已修复：generateTokens 加载用户角色写入 claims~~
- [x] **前端 admin 中间件不检查角色** — ~~已修复：`admin.ts` 现在检查 `isAdmin`~~
- [x] **前端 User 接口缺少 `role` 字段** — ~~已修复：User interface 增加了 `roles: string[]`~~

### P1 — 高危（HIGH）

- [ ] **Logout 不失效 JWT** — 删除了 Session 记录，但 JWT 本身还有 15 分钟有效期，无 token 黑名单机制。被盗 token 登出后仍可使用。
  - 修复：在 Redis 中维护 token 黑名单，auth 中间件检查黑名单

- [x] **前端 Cookie 无 `httpOnly` 标志** — ~~已修复：refresh_token 改为后端 Set-Cookie httpOnly cookie~~

- [ ] **Session 表存储了完整 JWT** — `SessionToken` 字段存的是 JWT 本身，而非独立的 session ID。
  - 修复：生成独立 session token（如 UUID），不用 JWT 本身

- [ ] **密码重置 token 先创建再发邮件** — 如果邮件发送失败，token 已入库但用户收不到邮件。
  - 修复：使用事务，邮件失败时回滚 token 创建

- [ ] **登录端点无独立限流** — 使用全局 100 req/min，应该用更严格的限流防暴力破解。
  - 修复：对 `/auth/login` 使用 StrictRateLimit

### P2 — 中等（MODERATE）

- [ ] **注册存在 TOCTOU 竞态** — 捕获 DB 唯一约束错误并转为友好提示
- [ ] **密码最低长度仅 6 位** — 偏弱，建议至少 8 位
- [ ] **迁移用户无需旧密码即可改密** — ChangePassword 跳过旧密码验证
- [ ] **JWT Expires 配置项从未使用** — 代码里全是硬编码 15min/7day
- [ ] **CORS 硬编码了生产域名** — `kungal.com` 和 `moyu.moe` 写死在中间件里
- [ ] **无审计日志** — 所有关键操作无日志记录

---

## 三、与 plans.md 的差距

| plans.md 设计 | 实际状态 | 优先级 |
|--------------|---------|--------|
| OAuth 2.0 + PKCE 完整流程 | ✅ 已注册路由 | — |
| RBAC（Role + Permission） | ✅ 已接入 JWT | — |
| 用户迁移脚本 | ✅ 已实现（含 user_site_data + follows） | — |
| DB 迁移 + Seed | ✅ 已实现 | — |
| asynq 任务队列 | 完全未实现 | P2 |
| 对象存储（临时/正式 bucket） | 完全未实现 | P2 |
| ClamAV 病毒扫描 | stub | P3 |
| AI 审核（OpenAI/Anthropic） | stub，返回硬编码 approved | P3 |
| 管理后台完整功能 | 前端大量页面是空壳或假数据 | P1 |

---

## 四、未实现的 Handler Stub 清单

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
- [ ] 用户管理页面：取消注释 `fetchUsers()`，后端已有 admin 用户列表接口
- [ ] 站点管理页面：实现创建/编辑弹窗
- [ ] OAuth 客户端页面：实现创建弹窗
- [ ] 审核详情页 `/moderation/:id`
- [ ] 站点详情/编辑页 `/sites/:id`

---

## 七、建议修复顺序

1. ✅ ~~注册 OAuth 路由 + 修复角色写入 JWT（P0）~~
2. ✅ ~~修复前端 admin 权限校验 + httpOnly cookie（P0）~~
3. ✅ ~~完善迁移脚本（user_site_data + follows）~~
4. ✅ ~~移除 game/content/comment 模块~~
5. 补全 Site/OAuthClient 的 Create handler（OAuth 流程前置）
6. 实现 token 黑名单（用 Redis）
7. 补全用户列表接口 + 前端用户管理页
8. 基础设施（Queue/Storage）按需补全
