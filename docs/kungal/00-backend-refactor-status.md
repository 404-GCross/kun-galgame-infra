# 后端重构总状态

> 最后更新: 2026-04-12

## 架构概览

```
用户浏览器
    │
    ├── kungal.com (Nuxt 4 前端)
    │       │
    │       ├── apps/api (Go Fiber 后端)
    │       │     ├── 用户/认证 (Redis session)
    │       │     ├── 话题/评论/消息/工具集/网站/文档/管理
    │       │     └── galgame 交互 (like/comment/rating/resource)
    │       │           └── 调用 galgame service 获取元数据
    │       │
    │       └── galgame service (OAuth repo, 独立端口)
    │             ├── galgame 元数据 CRUD
    │             ├── tag/official/engine/series CRUD
    │             ├── PR 系统 + 编辑历史
    │             └── VNDB/DLSite/Bangumi 数据聚合
    │
    └── moyu.moe → 只读调用 galgame service 获取元数据
```

### 数据库拓扑

| 数据库 | 用途 | 读写方 |
|--------|------|--------|
| `kun_oauth_admin` | OAuth 用户、角色、站点数据 | OAuth 服务读写，galgame service 只读 |
| `kun_galgame_wiki` | galgame 元数据 (15 张表) | galgame service 读写 |
| kungal 主库 | 交互表 + topic + message + ... | kungal 后端读写 |
| moyu 主库 | patch + moyu 特有数据 | moyu 后端读写 |

三个库的 user_id 已通过迁移脚本全局同步。

## 迁移目标

将 Nuxt 4 Nitro 后端 (`apps/nitro-server/`) 完全替换为 Go Fiber 后端 (`apps/api/`)，前端 (`apps/web/`) 保持 Nuxt 4。galgame 元数据 CRUD 独立为 galgame service（OAuth repo）。

## 总体数字

| 维度 | Nitro (原) | Go (目标) | 已完成 |
|------|-----------|-----------|--------|
| API 端点 | ~194 | ~194 | ~16 |
| 数据模型 | 75 (Prisma) | 78 (GORM) | 78 (全部定义) |
| 业务模块 | 30 | 10 (合并) | 1 (user) |
| 工具函数 | 35 文件 | pkg/ | 部分 |
| WebSocket | 5 文件 | 0 | 0 |
| 定时任务 | 2 | 0 | 0 |
| 数据库迁移 | - | 4 SQL | 4 |

## 已完成 (Phase 1)

### 基础设施
- [x] Go 项目结构 (cmd/ internal/ pkg/ migrations/)
- [x] PostgreSQL + GORM 连接（连接池配置）
- [x] Redis 客户端
- [x] S3 存储客户端 (Upload/Delete)
- [x] 邮件客户端骨架
- [x] 统一错误码 (0/205/233) + 响应格式 `{code, message, data}`
- [x] slog 日志
- [x] 环境变量配置 (config.Load → error)
- [x] SQL 迁移工具 (cmd/migrate)
- [x] 4 个 SQL 迁移文件 (oauth_account, count_columns, jsonb, data_migration)

### 中间件
- [x] Auth (Redis session + OAuth token 自动刷新 + 分布式锁)
- [x] OptionalAuth
- [x] RequireRole(minRole)
- [x] CORS (环境变量配置)
- [x] RateLimit (Redis per-user)
- [x] recover (panic 恢复)

### User 模块 (~16 端点)
- [x] OAuth 登录回调 (PKCE)
- [x] 登出 (Redis + OAuth revoke)
- [x] GET /api/auth/me
- [x] GET /api/user/:uid (公开资料)
- [x] POST /api/user/check-in (每日签到)
- [x] PUT /api/user/bio, username, email
- [x] GET /api/user/email, status
- [x] POST /api/user/avatar (S3 上传骨架, TODO: sharp resize)
- [x] GET /api/user/:uid/galgames, topics
- [x] PUT /api/user/:uid/ban, DELETE /api/user/:uid (管理员)

### 首页
- [x] GET /api/home (12 galgames + 10 topics 聚合)

### 前端适配
- [x] kunFetch / useKunFetch 统一请求层
- [x] OAuth PKCE 登录流程
- [x] 响应格式适配 (code/message/data 拆包)

## 未完成

### Phase 2: Galgame 元数据 → galgame service (OAuth repo)

在 OAuth repo 实现，独立数据库 `kun_galgame_wiki`：

- [ ] galgame service 基础框架 (cmd/galgame/main.go)
- [ ] 双数据库连接 (wiki 读写 + oauth 只读)
- [ ] JWT 认证中间件 (验证 OAuth Bearer Token)
- [ ] galgame 基础 CRUD (5 端点)
- [ ] galgame 元数据 tag/engine/official/series (24 端点)
- [ ] galgame PR + history + contributor (8 端点)
- [ ] galgame link + alias (5 端点)
- [ ] 数据迁移脚本 (kungal → kun_galgame_wiki)
- [ ] VNDB/DLSite/Bangumi 全量数据聚合

### Phase 2b: Galgame 交互 → kungal 后端 (apps/api)

- [ ] GalgameClient (HTTP client 调用 galgame service)
- [ ] galgame_stats 新表 + 回填
- [ ] galgame 互动 like/favorite (3 端点, 本地操作)
- [ ] galgame-comment (4 端点, 本地操作)
- [ ] galgame-resource (8 端点, 本地操作)
- [ ] galgame-rating (9 端点, 本地操作)
- [ ] galgame 详情聚合层 (galgame service 元数据 + 本地交互数据)

### Phase 3: Topic 论坛 (~28 端点)
- [ ] topic 基础 CRUD (5 端点)
- [ ] topic 互动 like/dislike/upvote/favorite (5 端点)
- [ ] topic-reply CRUD + 互动 (8 端点)
- [ ] topic-comment (3 端点)
- [ ] topic-poll 投票 (7 端点)
- [ ] topic 状态 hide/pin/best-answer

### Phase 4: 辅助模块 (~80 端点)
- [ ] message 消息通知 (8 端点)
- [ ] admin 管理后台 (6 端点)
- [ ] doc 文档系统 (12 端点)
- [ ] toolset 工具集 (17 端点, 含大文件上传)
- [ ] website 网站收录 (8 端点)
- [ ] website-category + website-tag (6 端点)
- [ ] update 更新日志 (6 端点)
- [ ] ranking 排行榜 (3 端点)
- [ ] activity 活动流 (2 端点)
- [ ] search 搜索 (Meilisearch 集成)
- [ ] report 举报 (1 端点)
- [ ] image 图片上传 (1 端点)
- [ ] RSS (2 端点)
- [ ] unmoe, section, category, resource 首页 (各 1 端点)

### Phase 5: 基础设施收尾
- [ ] 定时任务 (每日重置签到/上传计数, 清理过期资源)
- [ ] WebSocket / Socket.IO (私聊消息)
- [ ] Markdown 渲染 (goldmark + 自定义插件)
- [ ] Meilisearch 索引
- [ ] CDN 缓存清除 (Cloudflare API)
- [ ] 邮件发送验证码
- [ ] 前端全量适配 (所有页面切换到 kunFetch)
