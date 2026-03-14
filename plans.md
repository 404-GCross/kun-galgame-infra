# KUN OAuth Admin - 项目规划文档

> 本文档记录了项目的所有技术决策、架构设计和迁移策略，供 Claude Code 在上下文耗尽时参考。

## 一、项目背景

### 1.1 现有系统

目前有两个独立运营的网站：

| 网站 | 技术栈 | 数据库 | 主要功能 |
|------|--------|--------|----------|
| kungal-nuxt | Nuxt4 + Prisma + PostgreSQL + Redis | `kungalgame` | 游戏本体、社区讨论、Wiki |
| moyu-nextjs | Next.js + Prisma + PostgreSQL + Redis | `kungalgame_patch` | 游戏补丁、DLC、资源分发 |

两个网站日活合计约 60k，账户系统各自独立，导致用户需要分别注册，部分功能无法联动。

### 1.2 项目目标

构建一个统一的 Account Service（OAuth 管理系统），实现：

1. **统一身份认证**：两个网站 + 未来的 App / 桌面端共用一套账户
2. **OAuth2 支持**：JWT + refresh token rotation + PKCE
3. **多站点管理**：每个用户在不同站点可以有不同的角色和数据
4. **内容审核**：统一的 AI 审核服务（文本、图片）
5. **文件分发**：游戏/补丁上传、校验、查毒、分发管理

### 1.3 未来规划

- **Go Fiber 重构**：用 Go Fiber 重构 kungal-nuxt 和 moyu-nextjs 的后端（已确定）
- **移动端 App**：iOS / Android，使用 Flutter 开发
- **桌面端**：Windows / macOS / Linux，使用 Flutter 开发
- **更多网站**：其他共用账户系统的新网站

---

## 二、技术栈选型

### 2.1 后端

| 组件 | 选型 | 理由 |
|------|------|------|
| 语言 | Go | 统一技术栈，为未来重构做准备 |
| Web 框架 | Fiber v3 | 高性能，API 风格类似 Express |
| ORM | GORM | 开发效率高，团队熟悉 |
| 数据库 | PostgreSQL | 与现有系统一致 |
| 缓存 | Redis + Fiber Storage | Session、rate limit、任务队列 |
| 任务队列 | asynq | 轻量级，与 Redis 集成好 |
| 配置管理 | godotenv | 读取现有 .env 文件格式 |
| 密码哈希 | argon2 (matthewhartstonge) | 比 bcrypt 更安全 |
| JWT | golang-jwt/jwt/v5 | 标准 JWT 库 |
| 参数验证 | go-playground/validator | 结构体验证，与 Fiber 配合好 |
| Markdown | goldmark | 内容解析 |

### 2.2 核心依赖

```go
require (
    github.com/go-playground/validator/v10 v10.30.1
    github.com/gofiber/fiber/v3 v3.1.0
    github.com/gofiber/storage/redis/v3 v3.4.3
    github.com/golang-jwt/jwt/v5 v5.3.1
    github.com/joho/godotenv v1.5.1
    github.com/matthewhartstonge/argon2 v1.4.6
    github.com/yuin/goldmark v1.7.16
    gorm.io/datatypes v1.2.7
    gorm.io/driver/postgres v1.6.0
    gorm.io/gorm v1.31.1
)
```

### 2.3 前端（管理后台）

| 组件 | 选型 | 理由 |
|------|------|------|
| 框架 | Nuxt 4 | 与 kungal-nuxt 保持一致 |
| UI 组件 | 复用 apps/web/components/kun | 已有组件库 |
| 状态管理 | Pinia | Nuxt 官方推荐 |
| HTTP 客户端 | $fetch (Nuxt 内置) | 统一封装 |

### 2.4 未来客户端

| 平台 | 技术栈 |
|------|--------|
| iOS | Flutter |
| Android | Flutter |
| Desktop | Flutter |

### 2.5 架构模式

**Modular Monolith**：单一进程，模块通过 interface 隔离，未来可拆分为微服务。

---

## 三、项目结构

### 3.1 Monorepo 结构

```
kun-oauth-admin/
├── apps/
│   ├── api/          # Go Fiber 后端
│   └── web/          # Nuxt 4 前端
├── packages/         # 共享 TS 类型（如需要）
├── data/             # 参考数据（现有网站的 Prisma schema）
├── Makefile          # 统一构建入口
├── pnpm-workspace.yaml
└── plans.md          # 本文档
```

**重要**：`pnpm-workspace.yaml` 只包含前端相关包，不包含 Go 后端：

```yaml
packages:
  - 'apps/web'
  - 'packages/*'
```

### 3.2 Go 后端结构

```
apps/api/
├── cmd/
│   ├── server/main.go      # HTTP 服务入口
│   └── worker/main.go      # 任务队列消费者入口
├── internal/
│   ├── app/
│   │   ├── app.go          # 应用初始化、依赖注入
│   │   └── router.go       # 路由注册
│   ├── platform/           # 业务模块
│   │   ├── auth/           # 认证模块
│   │   ├── site/           # 站点管理
│   │   ├── game/           # 游戏元数据
│   │   ├── content/        # 内容管理
│   │   ├── comment/        # 评论管理
│   │   ├── artifact/       # 文件分发
│   │   └── moderation/     # 内容审核
│   ├── infrastructure/     # 基础设施
│   │   ├── database/       # PostgreSQL 连接
│   │   ├── cache/          # Redis 连接
│   │   ├── queue/          # asynq 任务队列
│   │   └── storage/        # 对象存储
│   └── middleware/         # HTTP 中间件
├── pkg/                    # 跨模块共享代码
│   ├── errors/             # 错误码定义
│   ├── response/           # 统一响应格式
│   ├── config/             # 配置加载
│   ├── logger/             # 日志
│   └── utils/              # 工具函数
├── migrations/             # 手动 SQL 迁移（生产环境破坏性变更）
└── scripts/                # 迁移脚本（用户数据迁移等）
```

### 3.3 每个业务模块的内部结构

```
internal/platform/auth/
├── handler/           # HTTP 处理器
│   └── auth_handler.go
├── service/           # 业务逻辑
│   └── auth_service.go
├── repository/        # 数据访问
│   └── user_repository.go
├── model/             # 数据模型
│   ├── user.go
│   └── session.go
└── dto/               # 请求/响应结构
    ├── login_dto.go
    └── register_dto.go
```

---

## 四、数据库设计

### 4.1 数据库拓扑

三个独立的 PostgreSQL 数据库：

| 数据库 | 用途 | 连接信息 |
|--------|------|----------|
| `kun_oauth_admin` | Account Service | localhost:5432 |
| `kungalgame` | kungal-nuxt 业务数据 | localhost:5432 |
| `kungalgame_patch` | moyu-nextjs 业务数据 | localhost:5432 |

**关键决策**：三个库保持独立，两个网站的 user_id 外键改为存储 Account Service 的 `user_uuid`（String），通过应用层保证一致性，不再是数据库外键约束。

### 4.2 Account Service 核心 Model（GORM）

#### User（核心身份表）

```go
type User struct {
    ID          uint           `gorm:"primaryKey"`
    UUID        string         `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()"`
    Name        string         `gorm:"size:17;uniqueIndex;not null"`
    Email       string         `gorm:"size:255;uniqueIndex;not null"`
    Password    *string        `gorm:"size:255"` // 可为 NULL，迁移用户初始为空
    Avatar      string         `gorm:"size:255;default:''"`
    Bio         string         `gorm:"size:107;default:''"`
    Moemoepoint int            `gorm:"default:0"`
    Status      int            `gorm:"default:0"` // 0: normal, 1: banned
    IP          string         `gorm:"size:45;default:''"`
    CreatedAt   time.Time
    UpdatedAt   time.Time

    // Relations
    SiteData      []UserSiteData `gorm:"foreignKey:UserID"`
    Sessions      []Session      `gorm:"foreignKey:UserID"`
    OAuthAccounts []OAuthAccount `gorm:"foreignKey:UserID"`
    Followers     []UserFollow   `gorm:"foreignKey:FollowingID"`
    Following     []UserFollow   `gorm:"foreignKey:FollowerID"`
}
```

**Password 字段说明**：
- 新注册用户：argon2 标准格式（matthewhartstonge/argon2）
- 迁移用户：初始为 NULL，首次登录需要通过邮箱重置密码

#### Site（站点表）

```go
type Site struct {
    ID          uint   `gorm:"primaryKey"`
    Name        string `gorm:"size:50;not null"`
    Domain      string `gorm:"size:255;uniqueIndex;not null"`
    Description string `gorm:"type:text;default:''"`
    CreatedAt   time.Time

    // Relations
    UserSiteData []UserSiteData `gorm:"foreignKey:SiteID"`
    OAuthClients []OAuthClient  `gorm:"foreignKey:SiteID"`
}

// 初始数据：kungal (www.kungal.com), moyu (www.moyu.moe)
```

#### UserSiteData（用户站点数据）

```go
type UserSiteData struct {
    ID              uint            `gorm:"primaryKey"`
    UserID          uint            `gorm:"not null;index"`
    SiteID          uint            `gorm:"not null;index"`
    Role            int             `gorm:"default:1"`
    Status          int             `gorm:"default:0"`
    DailyCheckIn    int             `gorm:"default:0"`
    DailyImageCount int             `gorm:"default:0"`
    Extra           datatypes.JSON  `gorm:"type:jsonb;default:'{}'"` // 站点特有字段
    CreatedAt       time.Time
    UpdatedAt       time.Time

    // Relations
    User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
    Site Site `gorm:"foreignKey:SiteID;constraint:OnDelete:CASCADE"`
}

// Extra 字段用途：
// kungal: { "daily_toolset_upload_count": 0 }
// moyu: { "daily_upload_size": 0, "last_login_time": "" }
```

#### Session（登录会话）

```go
type Session struct {
    ID           uint      `gorm:"primaryKey"`
    UserID       uint      `gorm:"not null;index"`
    SessionToken string    `gorm:"size:255;uniqueIndex;not null"`
    RefreshToken string    `gorm:"size:255;uniqueIndex;not null"`
    UserAgent    string    `gorm:"type:text;default:''"`
    IPAddress    string    `gorm:"size:45;default:''"`
    ExpiresAt    time.Time `gorm:"not null"`
    CreatedAt    time.Time

    // Relations
    User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}
```

#### OAuthClient（OAuth 客户端）

```go
type OAuthClient struct {
    ID           string         `gorm:"size:50;primaryKey"`
    SiteID       *uint          `gorm:"index"`
    Name         string         `gorm:"size:100;not null"`
    Secret       string         `gorm:"size:255;not null"`
    RedirectURIs datatypes.JSON `gorm:"type:jsonb;not null"` // ["https://www.kungal.com/callback"]
    Grants       datatypes.JSON `gorm:"type:jsonb;not null"` // ["authorization_code", "refresh_token"]
    CreatedAt    time.Time

    // Relations
    Site *Site `gorm:"foreignKey:SiteID"`
}
```

#### OAuthAccount（第三方登录）

```go
type OAuthAccount struct {
    ID                uint    `gorm:"primaryKey"`
    UserID            uint    `gorm:"not null;index"`
    Provider          string  `gorm:"size:50;not null"`  // 'google', 'github', 'discord'
    ProviderAccountID string  `gorm:"size:255;not null"`
    AccessToken       *string `gorm:"type:text"`
    RefreshToken      *string `gorm:"type:text"`
    ExpiresAt         *int
    CreatedAt         time.Time

    // Relations
    User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// 复合唯一索引：(provider, provider_account_id)
```

#### UserFollow（社交关系，仅 moyu 数据）

```go
type UserFollow struct {
    ID          uint `gorm:"primaryKey"`
    FollowerID  uint `gorm:"not null;index"`
    FollowingID uint `gorm:"not null;index"`
    CreatedAt   time.Time

    // Relations
    Follower  User `gorm:"foreignKey:FollowerID;constraint:OnDelete:CASCADE"`
    Following User `gorm:"foreignKey:FollowingID;constraint:OnDelete:CASCADE"`
}

// 复合唯一索引：(follower_id, following_id)
```

#### Role 和 Permission（RBAC）

```go
type Role struct {
    ID          uint   `gorm:"primaryKey"`
    Name        string `gorm:"size:50;uniqueIndex;not null"`
    Description string `gorm:"type:text;default:''"`

    Permissions []Permission `gorm:"many2many:role_permissions;"`
}

type Permission struct {
    ID          uint   `gorm:"primaryKey"`
    Name        string `gorm:"size:100;uniqueIndex;not null"`
    Description string `gorm:"type:text;default:''"`

    Roles []Role `gorm:"many2many:role_permissions;"`
}
```

### 4.3 错误码规范

按模块分段，定义在 `pkg/errors/codes.go`：

| 模块 | 范围 | 示例 |
|------|------|------|
| Auth | 10000-19999 | 10001: Unauthorized, 10002: InvalidToken |
| Game | 20000-29999 | 20001: GameNotFound |
| Content | 30000-39999 | 30001: ContentNotFound |
| Comment | 40000-49999 | 40001: CommentNotFound |
| Artifact | 50000-59999 | 50001: ArtifactNotFound |
| Moderation | 60000-69999 | 60001: ModerationPending |
| Site | 70000-79999 | 70001: SiteNotFound |

---

## 五、用户迁移方案

### 5.1 迁移原则

| 字段 | 处理方式 |
|------|----------|
| name（用户名） | 保留 kungal-nuxt |
| email | 保留 kungal-nuxt |
| password | 保留 moyu-nextjs（argon2），kungal 的 bcrypt 作为过渡 |
| avatar | 保留 kungal-nuxt，若为空则用 moyu |
| bio | 保留 kungal-nuxt，若为空则用 moyu |
| moemoepoint | 两边加和 |
| created_at | 保留较早的时间 |
| daily_* 字段 | 放入 UserSiteData，每站点独立 |
| 社交关系 | 仅迁移 moyu-nextjs 的数据（kungal 无实际数据） |

### 5.2 迁移流程

```
第一步：准备工作
├── 备份三个数据库
├── 在 Account Service 创建 sites 表数据
└── 生成 OAuth client（kungal_web, moyu_web）

第二步：导入 kungal-nuxt 用户
├── 遍历 kungalgame.user 表
├── 创建 users 记录（password = NULL）
├── 创建 user_site_data 记录（site=kungal）
└── 记录映射：kungal_old_id → new_uuid

第三步：合并 moyu-nextjs 用户
├── 遍历 kungalgame_patch.user 表
├── 按 email 查找是否已存在
│
├── 如果存在（同邮箱冲突）：
│   ├── users.moemoepoint += moyu.moemoepoint
│   ├── 如果 avatar/bio 为空，使用 moyu 的
│   ├── created_at 取较早的
│   ├── 创建 user_site_data 记录（site=moyu）
│   └── 记录映射：moyu_old_id → existing_uuid
│
└── 如果不存在：
    ├── 创建 users 记录（password = NULL）
    ├── 创建 user_site_data 记录（site=moyu）
    └── 记录映射：moyu_old_id → new_uuid

第四步：迁移社交关系
├── 遍历 kungalgame_patch.user_follow_relation
├── 使用映射表转换 follower_id 和 following_id
└── 插入 user_follows 表

第五步：更新两个网站的外键
├── kungalgame 所有包含 user_id 的表
│   ├── 新增 account_user_uuid 列
│   ├── 使用映射表填充 uuid
│   └── （可选）删除原 user 表
│
└── kungalgame_patch 所有包含 user_id 的表
    ├── 新增 account_user_uuid 列
    ├── 使用映射表填充 uuid
    └── （可选）删除原 user 表
```

### 5.3 密码重置方案

由于 kungal-nuxt（bcrypt）和 moyu-nextjs（自定义 argon2）的密码格式都与新系统不兼容，
**所有迁移用户的 password 字段初始为 NULL，必须通过邮箱重置密码**。

```
迁移用户首次登录流程：

用户输入 email
        │
        ▼
检查 password 是否为 NULL
        │
        ├── password = NULL（迁移用户）
        │   ├── 提示「账户已迁移，请重置密码」
        │   ├── 发送重置密码邮件
        │   ├── 用户点击链接设置新密码
        │   └── 新密码使用 argon2 标准格式存储
        │
        └── password != NULL（已重置或新用户）
            └── 正常验证 argon2 密码

密码格式（统一）：
$argon2id$v=19$m=65536,t=3,p=4$salt$hash
```

**优势**：
- 代码简单：只需支持一种密码格式（argon2 标准）
- 安全性统一：所有用户使用相同的安全参数
- 无兼容性负担：不需要维护多种验证逻辑

### 5.4 迁移后网站改造

两个网站需要改造的部分：

1. **登录/注册**：调用 Account Service API
2. **获取用户信息**：通过 uuid 查询 Account Service
3. **本地用户关联**：业务表通过 `account_user_uuid` 关联
4. **Session 验证**：验证 JWT token（可本地验证签名）

---

## 六、认证方案

### 6.1 Token 设计

| Token | 存储位置 | 有效期 | 用途 |
|-------|----------|--------|------|
| Access Token | 内存 / localStorage | 15 分钟 | API 请求认证 |
| Refresh Token | HttpOnly Cookie | 7 天 | 刷新 Access Token |

### 6.2 Access Token (JWT) 结构

```json
{
  "sub": "user_uuid",
  "email": "user@example.com",
  "name": "username",
  "site": "kungal",
  "role": 1,
  "iat": 1234567890,
  "exp": 1234568790
}
```

### 6.3 Refresh Token Rotation

每次使用 refresh token 刷新时：
1. 验证 refresh token 有效性
2. 生成新的 access token 和 refresh token
3. 旧 refresh token 立即失效
4. 返回新的 token 对

### 6.4 登录流程（Web）

```
用户访问 kungal.com
        │
        ▼
点击登录 → 跳转到 Account Service /login
        │
        ▼
输入账号密码 → 验证成功
        │
        ▼
生成 authorization code → 重定向回 kungal.com/callback?code=xxx
        │
        ▼
kungal.com 后端用 code 换取 tokens
        │
        ▼
返回 access_token + refresh_token（HttpOnly Cookie）
```

---

## 七、API 设计

### 7.1 Auth API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/v1/auth/login | 登录 |
| POST | /api/v1/auth/register | 注册 |
| POST | /api/v1/auth/logout | 登出 |
| POST | /api/v1/auth/refresh | 刷新 token |
| GET | /api/v1/auth/me | 获取当前用户 |
| POST | /api/v1/auth/change-password | 修改密码 |
| POST | /api/v1/auth/forgot-password | 忘记密码 |
| POST | /api/v1/auth/reset-password | 重置密码 |

### 7.2 OAuth API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/oauth/authorize | 授权端点 |
| POST | /api/v1/oauth/token | Token 端点 |
| GET | /api/v1/oauth/userinfo | 用户信息端点 |
| POST | /api/v1/oauth/revoke | 撤销 token |

### 7.3 Admin API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/admin/users | 用户列表 |
| GET | /api/v1/admin/users/:id | 用户详情 |
| PATCH | /api/v1/admin/users/:id | 更新用户 |
| POST | /api/v1/admin/users/:id/ban | 封禁用户 |
| GET | /api/v1/admin/sites | 站点列表 |
| POST | /api/v1/admin/sites | 创建站点 |
| GET | /api/v1/admin/moderation/jobs | 审核队列 |
| POST | /api/v1/admin/moderation/jobs/:id/review | 人工审核 |

---

## 八、内容审核系统

### 8.1 架构

```
内容提交 → 本地规则（关键词/正则）→ 轻量 API（Moderation API）→ 强 LLM（GPT-4o）
              │                           │                            │
              ▼                           ▼                            ▼
         明显违规直接拒绝           大部分内容处理              模糊 case 处理
         （成本：0）               （成本：低）                （成本：高）
```

### 8.2 审核策略

| 内容类型 | 策略 |
|----------|------|
| 评论、回复 | 先显示，异步审核，违规后删除 |
| 文章、游戏介绍 | 先进入 pending，审核通过后显示 |
| 用户头像、昵称 | 同步审核，注册时卡住 |
| 上传文件 | 必须同步卡住，绝对不能先放行 |

### 8.3 缓存策略

- 缓存 key：`hash(content):policy_v{version}`
- 策略版本变更时，旧缓存自动失效

---

## 九、文件上传系统

### 9.1 流程

```
1. 客户端申请上传 → API 验证权限、类型、大小
2. 生成 Presigned URL → 指向临时 bucket
3. 客户端直传到临时 bucket
4. 上传完成回调 API
5. Worker 处理：校验、查毒、manifest 验证
6. 处理通过 → 移到正式 bucket
7. 处理失败 → 删除，通知用户
```

### 9.2 关键设计

- **临时 bucket 和正式 bucket 分开**：未校验文件不进入正式存储
- **断点续传**：使用 Multipart Upload，每片 10MB
- **病毒扫描**：ClamAV，扫描结果作为参考，配合人工审核

---

## 十、执行计划

### 10.1 开发顺序（按业务优先级）

1. **pkg/ 层**：errors、response、config、logger、utils
2. **infrastructure/**：database、cache、queue
3. **Auth 模块**：完整实现登录、注册、token 刷新
4. **Site 模块**：站点管理
5. **迁移脚本**：用户数据迁移、ID 映射
6. **Content + Comment 模块**：内容管理
7. **Moderation 模块**：内容审核
8. **Game 模块**：游戏元数据
9. **Artifact 模块**：文件上传分发

### 10.2 里程碑

| 阶段 | 目标 | 验收标准 |
|------|------|----------|
| M1 | 基础架构 + Auth | 能完成登录注册，token 刷新 |
| M2 | 用户迁移 | 两个网站用户成功合并，能登录 |
| M3 | 网站接入 | 两个网站切换到 Account Service |
| M4 | 管理后台 | 用户管理、站点管理功能完成 |
| M5 | 审核系统 | AI 审核 + 人工审核队列 |
| M6 | 文件系统 | 上传、校验、分发完整流程 |

---

## 十一、环境变量

Account Service 使用的环境变量（`apps/api/.env`）：

```bash
# Server
KUN_FIBER_SERVER_PORT=9277
KUN_FRONTEND_CORS_ORIGIN=http://127.0.0.1:9420
KUN_ENV="development"
KUN_SITE_URL=http://localhost:9277

# PostgreSQL
KUN_PG_HOST=localhost
KUN_PG_PORT=5432
KUN_PG_USER=postgres
KUN_PG_PASSWORD=******
KUN_PG_DATABASE=kun_oauth_admin
KUN_PG_SSLMODE=disable
KUN_PG_TIMEZONE=Asia/Shanghai

# Redis
REDIS_ENABLED=false
REDIS_HOST=localhost
REDIS_PORT=6379
REDIS_USERNAME=
REDIS_PASSWORD=
REDIS_DB=0

# JWT
JWT_SECRET=******
JWT_COOKIE_NAME=kun_love_ren_token
JWT_EXPIRES=90d
```

---

## 十二、代码规范

### 12.1 Go 代码规范

- 使用 GORM 作为 ORM，配合 `gorm.io/datatypes` 处理 JSON 字段
- 数据库迁移使用 GORM AutoMigrate（开发）+ 手动 SQL（生产破坏性变更）
- 依赖注入使用手动构造，不引入 wire/fx
- 请求参数验证使用 `go-playground/validator`，定义在 DTO struct tag 中
- 每个 service 目录下需要 `_test.go` 骨架文件
- 错误处理使用 `pkg/errors` 统一封装
- 密码哈希使用 `matthewhartstonge/argon2`

### 12.2 前端代码规范

- 使用 TypeScript
- 状态管理使用 Pinia
- API 调用使用 `services/api.ts` 统一封装
- 路由守卫使用 Nuxt middleware

### 12.3 提交规范

```
feat: 新功能
fix: 修复 bug
docs: 文档更新
refactor: 重构
test: 测试
chore: 构建/工具
```

---

## 附录 A：现有网站 Schema 参考

详见 `data/` 目录：
- `data/kungal-nuxt/schema/` - kungal-nuxt 的 Prisma schema
- `data/moyu-nextjs/schema/` - moyu-nextjs 的 Prisma schema

## 附录 B：ID 映射表结构

迁移时生成的映射表：

```sql
-- kungal 用户 ID 映射
CREATE TABLE migration_kungal_user_mapping (
    old_id INT PRIMARY KEY,
    new_uuid UUID NOT NULL
);

-- moyu 用户 ID 映射
CREATE TABLE migration_moyu_user_mapping (
    old_id INT PRIMARY KEY,
    new_uuid UUID NOT NULL
);
```

## 附录 C：关键决策记录

| 决策点 | 选择 | 理由 |
|--------|------|------|
| Web 框架 | Fiber v3 | 高性能，API 风格类似 Express |
| ORM | GORM | 团队熟悉，开发效率高 |
| OAuth 实现 | 自己实现 JWT + PKCE | 规模不需要 Hydra |
| 数据库迁移 | GORM AutoMigrate | 开发便捷，生产环境手动处理破坏性变更 |
| 依赖注入 | 手动构造 | 单人项目，不需要框架 |
| 配置管理 | godotenv | 与现有 .env 格式一致 |
| 参数验证 | go-playground/validator | 结构体 tag 验证，简洁 |
| 任务队列 | asynq | 轻量，与 Redis 集成好 |
| 密码哈希 | argon2 (matthewhartstonge) | 比 bcrypt 更安全，使用 PHC 标准格式 |
| JWT | golang-jwt/jwt/v5 | 标准库 |
| 密码迁移 | 所有用户重置密码 | 简化实现，两个网站的旧格式都不兼容 |
| 用户名冲突 | 保留 kungal | 以 kungal 为主 |
| 数据库拓扑 | 三库独立 | 微服务架构 |
| 外键处理 | 存 uuid，应用层保证 | 跨库无法用外键约束 |
| 未来客户端 | Flutter | iOS/Android/Desktop 统一技术栈 |
