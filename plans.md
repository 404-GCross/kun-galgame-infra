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
4. **内容审核（共享服务）**：统一的 AI 审核服务（文本、图片），各站点调用
5. **文件分发（共享服务）**：上传、校验、查毒、分发管理，各站点调用

> **职能分离原则**：本系统只管理身份、站点、角色及跨站共享服务（审核/文件）。
> 站点业务数据（话题、评论、游戏条目、补丁等）由各网站自行管理，通过 JWT 验证管理员身份。

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
│   │   ├── auth/           # 认证模块（用户、会话、OAuth）
│   │   ├── site/           # 站点管理（站点、OAuth Client、角色）
│   │   ├── artifact/       # 文件分发（共享服务）
│   │   └── moderation/     # 内容审核（共享服务）
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
    Roles         []Role         `gorm:"many2many:user_roles;"`  // 全局角色
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
| OAuth | 15000-15999 | 15001: InvalidClient, 15003: InvalidCode |
| Artifact | 50000-59999 | 50001: ArtifactNotFound |
| Moderation | 60000-69999 | 60001: ModerationPending |
| Site | 70000-79999 | 70001: SiteNotFound |

> Game (20000-29999)、Content (30000-39999)、Comment (40000-49999) 错误码段已移除，
> 保留范围供各站点自行在其后端使用。

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
├── 生成 OAuth client（kungal_web, moyu_web）
└── 重置 users_id_seq 序列（ALTER SEQUENCE users_id_seq RESTART WITH 1）

第二步：合并两站用户（内存中）
├── 遍历 kungalgame.user 表，按 email 建立索引
├── 遍历 kungalgame_patch.user 表，按 email 合并
│   ├── 同邮箱冲突：kungal 优先（name/email/avatar/bio），moyu 补充
│   │   ├── moemoepoint 两边加和
│   │   ├── avatar/bio 若 kungal 为空则用 moyu
│   │   └── created_at 取较早的时间戳
│   └── 无冲突：作为 moyu-only 用户
└── 输出统一的合并用户列表

第三步：按 created_at 排序后插入
├── 对合并用户列表按 created_at 升序排序
├── 按顺序逐个插入（最早注册 → ID=1，依此类推）
├── 每个用户在事务内同时创建：
│   ├── users 记录（password = NULL）
│   ├── user_site_data 记录（kungal 和/或 moyu）
│   └── user_migrations 映射记录
└── 结果：用户 ID 严格按注册时间从早到晚递增

第四步：迁移社交关系
├── 遍历 kungalgame_patch.user_follow_relation
├── 使用映射表转换 follower_id 和 following_id
└── 插入 user_follows 表

第五步：映射站点角色 → 全局角色（user_roles）
├── 遍历所有 user_site_data 记录
├── 按以下规则确定每个用户的最高全局角色：
│
│   kungal-nuxt 角色定义：1=用户, 2=管理员, 3=超级管理员
│   moyu-nextjs 角色定义：1=用户, 2=创作者, 3=管理员, 4=超级管理员
│
│   映射规则：
│   ├── kungal role=3 (超管) → admin
│   ├── kungal role=2 (管理) → moderator
│   ├── moyu role=4 (超管)   → admin
│   ├── moyu role=3 (管理)   → moderator
│   ├── moyu role=2 (创作者) → 不映射（业务角色，保留在 user_site_data.role）
│   └── role=1 (普通用户)    → 不映射
│
├── 同一用户在两个站点都有管理角色时，取较高的
│   例：kungal 超管 + moyu 管理 → admin（取最高）
│
└── 写入 user_roles 关联表（ON CONFLICT DO NOTHING 防重复）

注：此映射仅在迁移时一次性执行。
    后续新用户的全局角色通过管理后台手动分配。
    user_site_data.role 保持原值不变，各站点仍通过它控制站内业务权限。

第六步：更新两个网站的外键
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

### 5.3 密码无感迁移方案

Users 表包含三个密码列：

| 列 | 格式 | 来源 | 生命周期 |
|---|---|---|---|
| `password` | argon2 PHC 标准格式 | 新系统 | 永久 |
| `kungal_password` | bcrypt `$2b$07$...` | kungal-nuxt | 迁移后 6 个月删除 |
| `moyu_password` | 自定义 `salt_hex:hash_hex` | moyu-nextjs（argon2id, t=2, m=8192, p=3, keyLen=32） | 迁移后 6 个月删除 |

```
用户登录流程：

输入 email + password
        │
        ▼
查找用户
        │
        ├── password != NULL（新密码已设置）
        │   └── 验证 argon2 PHC 格式 → 成功/失败
        │
        ├── password == NULL, 有旧密码
        │   ├── 尝试 kungal_password（bcrypt.Compare）
        │   │   └── 匹配 → hash 新密码存入 password，清除两个旧列 → 登录成功
        │   │
        │   ├── 尝试 moyu_password（argon2id, 解析 salt_hex:hash_hex）
        │   │   └── 匹配 → hash 新密码存入 password，清除两个旧列 → 登录成功
        │   │
        │   └── 都不匹配 → 密码错误
        │
        └── password == NULL, 无旧密码
            └── 提示需要重置密码
```

**无需额外服务**：bcrypt 和 argon2id 验证均由 Go 标准库（`golang.org/x/crypto/bcrypt` 和 `golang.org/x/crypto/argon2`）原生支持。

**6 个月后清理**：
1. 仍然 `password = NULL` 的用户需走忘记密码流程
2. 删除 `kungal_password` 和 `moyu_password` 列
3. 删除旧密码验证代码（`VerifyBcryptPassword`、`VerifyMoyuPassword`）

**新注册用户密码格式**（统一）：
```
$argon2id$v=19$m=65536,t=3,p=4$salt$hash
```

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
  "roles": ["admin"],
  "iat": 1234567890,
  "exp": 1234568790
}
```

> `roles` 为全局角色数组（`admin`/`moderator`/`user`），来自 `user_roles` 多对多关联表。
> 各站点的业务角色（如 moyu 的 publisher）存在 `user_site_data.role` 中，不进入 JWT。

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
| GET | /api/v1/admin/users/:uuid | 用户详情 |
| PATCH | /api/v1/admin/users/:uuid | 更新用户 |
| POST | /api/v1/admin/users/:uuid/ban | 封禁用户 |
| POST | /api/v1/admin/users/:uuid/unban | 解封用户 |
| DELETE | /api/v1/admin/users/:uuid/sessions | 强制登出 |

### 7.4 Site API（admin only）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/sites | 站点列表 |
| POST | /api/v1/sites | 创建站点 |
| GET | /api/v1/sites/:id | 站点详情 |
| PUT | /api/v1/sites/:id | 更新站点 |
| DELETE | /api/v1/sites/:id | 删除站点 |
| GET | /api/v1/oauth/clients | OAuth 客户端列表 |
| POST | /api/v1/oauth/clients | 创建 OAuth 客户端 |

### 7.5 Artifact API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/artifacts | 列表 |
| GET | /api/v1/artifacts/:id | 详情 |
| POST | /api/v1/artifacts | 上传 |
| DELETE | /api/v1/artifacts/:id | 删除 |
| GET | /api/v1/artifacts/:id/download | 下载 |

### 7.6 Moderation API（admin/moderator only）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/v1/moderation/jobs | 审核队列 |
| GET | /api/v1/moderation/jobs/:id | 审核详情 |
| POST | /api/v1/moderation/jobs/:id/review | 人工审核 |
| GET | /api/v1/moderation/policies | 审核策略 |

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

1. ✅ **pkg/ 层**：errors、response、config、logger、utils
2. ✅ **infrastructure/**：database、cache（queue/storage 待实现）
3. ✅ **Auth 模块**：登录、注册、token 刷新、OAuth 2.0、RBAC
4. ✅ **Site 模块**：站点管理（handler stub 待补全）
5. ✅ **迁移脚本**：用户数据迁移、user_site_data、follows、ID 映射
6. **Moderation 模块**：内容审核（共享服务，provider 待实现）
7. **Artifact 模块**：文件上传分发（共享服务，pipeline/storage 待实现）
8. **管理后台前端**：补全前端页面功能

> Game/Content/Comment 模块已移除 — 站点业务数据由各网站自行管理。

### 10.2 里程碑

| 阶段 | 目标 | 验收标准 | 状态 |
|------|------|----------|------|
| M1 | 基础架构 + Auth | 能完成登录注册，token 刷新 | ✅ 完成 |
| M2 | 用户迁移 | 两个网站用户成功合并（含 user_site_data + follows） | ✅ 完成 |
| M3 | 网站接入 | 两个网站切换到 Account Service | 进行中 |
| M4 | 管理后台 | 用户管理、站点管理、OAuth 客户端管理 | 进行中 |
| M5 | 审核系统（共享服务） | AI 审核 + 人工审核队列 | 待开始 |
| M6 | 文件系统（共享服务） | 上传、校验、分发完整流程 | 待开始 |

---

## 十一、环境变量

Account Service 使用的环境变量（`apps/api/.env`）：

```bash
# Server
KUN_FIBER_SERVER_PORT=9277
KUN_FRONTEND_CORS_ORIGIN=http://127.0.0.1:9420
KUN_ENV="development"
KUN_SITE_URL=http://127.0.0.1:9277

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

迁移时生成的统一映射表 `user_migrations`：

```go
type UserMigration struct {
    ID           uint      `gorm:"primaryKey"`
    UserID       uint      `gorm:"not null;index"`
    UserUUID     string    `gorm:"type:uuid;not null;index"`
    SourceDB     string    `gorm:"size:50;not null;index"` // "kungal" or "moyu"
    SourceUserID uint      `gorm:"not null"`
    SourceEmail  string    `gorm:"size:255;not null"`
    MergedFrom   *string   `gorm:"size:50"` // 合并时的次要来源
    CreatedAt    time.Time
}
```

> 同一个 UserID 可能有两条记录（kungal + moyu），通过 SourceDB 区分。

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
| 密码迁移 | 无感迁移（三列策略） | 旧密码原样复制，首次登录自动转为新格式，6 个月后清理 |
| 用户名冲突 | 保留 kungal | 以 kungal 为主 |
| 数据库拓扑 | 三库独立 | 微服务架构 |
| 外键处理 | 存 uuid，应用层保证 | 跨库无法用外键约束 |
| 未来客户端 | Flutter | iOS/Android/Desktop 统一技术栈 |
| 用户 ID 排序 | 按 created_at 升序分配 | 合并两站后排序插入，最早注册→ID=1，方便排序和分页 |
| 职能分离 | 移除 game/content/comment | 站点业务数据由各网站管理，本系统只管身份+共享服务 |
| 全局角色 | user_roles 多对多 | 与 user_site_data.role（站点级 int）分离，JWT 只含全局角色 |
| Refresh Token | httpOnly Cookie | 后端 Set-Cookie，前端不可读，防 XSS |
| 角色映射（迁移） | 超管→admin, 管理→moderator | 站点管理员自动获得全局审核权限，创作者不映射 |
