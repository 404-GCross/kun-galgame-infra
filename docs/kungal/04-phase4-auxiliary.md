# Phase 4: 辅助模块实现计划

> 预计端点数: ~80 | 前置: Phase 1 (已完成)

## 模块优先级排序

按业务价值和用户影响排序:

| 优先级 | 模块 | 端点数 | 理由 |
|--------|------|--------|------|
| 1 | message | 8 | 用户通知直接影响体验 |
| 2 | admin | 6 | 管理功能, 运营必须 |
| 3 | doc | 12 | 文档系统, 社区信息发布 |
| 4 | toolset | 17 | 工具集, 含大文件上传 |
| 5 | website | 14 | 网站收录 + category + tag |
| 6 | update | 6 | 更新日志 |
| 7 | ranking | 3 | 排行榜 |
| 8 | activity | 2 | 活动流 |
| 9 | 其他小模块 | 12 | search, report, rss, image 等 |

---

## 1. Message 消息模块 (8 端点)

模型已定义: `internal/message/model/message.go` (125 行, 8 模型)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/message | 消息列表 (按类型筛选) |
| DELETE | /api/message/:id | 删除消息 |
| GET | /api/message/admin | 管理员消息列表 |
| PUT | /api/message/admin/read | 标记已读 |
| GET | /api/message/nav/contact | 联系人摘要 |
| GET | /api/message/nav/system | 系统消息 |
| PUT | /api/message/system/read | 标记系统消息已读 |
| GET | /api/message/chat/history | 聊天历史 |

**消息类型:** replied, liked, upvoted, mentioned, requested, merged, declined

**关键:** 消息是由其他模块(topic reply, galgame PR 等)在事务中附带创建的。此模块主要是读取和管理。

---

## 2. Admin 管理模块 (6 端点)

模型已定义: `internal/admin/model/admin.go` (71 行, 4 模型)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/admin/overview/all | 概览数据 (用户/话题/galgame 总数) |
| GET | /api/admin/overview/stats | 统计 (近期注册/发帖趋势) |
| GET | /api/admin/setting/register | 获取注册设置 |
| PUT | /api/admin/setting/register | 更新注册设置 |
| GET | /api/admin/user | 用户列表 |
| GET | /api/admin/user/search | 搜索用户 |

**全部需要 RequireRole(3)**

---

## 3. Doc 文档模块 (12 端点)

模型已定义: `internal/doc/model/doc.go` (62 行, 4 模型)

**Article (5 端点):**
| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/doc/article | 文章列表 |
| GET | /api/doc/article/:slug | 按 slug 获取 |
| POST | /api/doc/article | 创建 (role >= 2) |
| PUT | /api/doc/article/:slug | 编辑 |
| DELETE | /api/doc/article/:slug | 删除 |

**Category (4 端点):** CRUD
**Tag (3 端点):** CRUD

**关键:** 文章需要 Markdown → HTML 转换

---

## 4. Toolset 工具集模块 (17 端点)

模型已定义: `internal/toolset/model/toolset.go` (143 行, 7 模型)

**基础 CRUD (3 端点):**
| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/toolset | 列表 |
| POST | /api/toolset | 创建 (+3 萌萌点) |
| GET/PUT/DELETE | /api/toolset/:id | CRUD |

**评分 (2 端点):**
- GET /api/toolset/:id/practicality — 获取评分分布
- PUT /api/toolset/:id/practicality — 提交/更新评分 (1-10)

**评论 (4 端点):** 自引用树形评论 (parent_id)

**资源 (4 端点):**
- POST /api/toolset/:id/resource — 创建资源
- PUT/DELETE /api/toolset/:id/resource — 编辑/删除
- GET /api/toolset/:id/resource/detail — 资源详情

**大文件上传 (4 端点):**
- POST /api/toolset/:id/upload/small — 小文件直传 S3
- POST /api/toolset/:id/upload/large — 分片上传初始化
- POST /api/toolset/:id/upload/complete — 分片上传完成
- POST /api/toolset/:id/upload/abort — 分片上传取消

**关键:** 大文件上传需要 S3 Multipart Upload API

---

## 5. Website 网站收录模块 (14 端点)

模型已定义: `internal/website/model/website.go` (119 行, 6 模型)

**Website (8 端点):**
| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/website | 列表 |
| POST | /api/website | 创建 |
| GET | /api/website/:domain | 详情 |
| PUT | /api/website/:domain | 编辑 |
| DELETE | /api/website/:domain | 删除 |
| PUT | /api/website/:domain/like | 点赞 |
| PUT | /api/website/:domain/favorite | 收藏 |
| POST/DELETE | /api/website/:domain/comment | 评论 |

**Category (2 端点):** GET/PUT
**Tag (4 端点):** CRUD

---

## 6. Update 更新日志模块 (6 端点)

使用 admin 模块的 UpdateLog 和 Todo 模型

**History (3 端点):** CRUD (admin only)
**Todo (3 端点):** CRUD (admin only)

---

## 7. Ranking 排行榜 (3 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/ranking/galgame | Galgame 排行 (by view/like/favorite/resource) |
| GET | /api/ranking/topic | Topic 排行 (by view) |
| GET | /api/ranking/user | 用户排行 (by moemoepoint) |

纯查询, 无写入

---

## 8. Activity 活动流 (2 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/activity | 活动列表 (按类型: galgame 创建/评分/资源等) |
| GET | /api/activity/timeline | 时间线视图 |

**关键:** 需要联合查询多个表 (galgame, topic, rating 等) 并按时间排序

---

## 9. 其他小模块

| HTTP | 路径 | 说明 | 复杂度 |
|------|------|------|--------|
| GET | /api/search | 统一搜索 (Meilisearch) | ⭐⭐⭐ |
| POST | /api/report/submit | 提交举报 | ⭐ |
| GET | /api/rss/galgame | Galgame RSS | ⭐⭐ |
| GET | /api/rss/topic | Topic RSS | ⭐⭐ |
| POST | /api/image/topic | 话题图片上传 (S3) | ⭐⭐ |
| GET | /api/resource | 资源首页数据 | ⭐ |
| GET | /api/section | 板块列表 | ⭐ |
| GET | /api/category | 分类列表 | ⭐ |
| GET | /api/unmoe | Unmoe 翻译器 | ⭐ |
| GET | /api/auth/email/send-code | 发送验证码 | ⭐⭐ |
| POST | /api/auth/email/verify | 验证码校验 | ⭐⭐ |

---

## 跨模块依赖

```
message ← topic (reply 创建消息), galgame (PR 创建消息), website (评论)
admin ← user (搜索/封禁), 所有模块 (概览统计)
search ← galgame, topic, user (Meilisearch 索引)
activity ← galgame, topic, rating (联合查询)
ranking ← galgame, topic, user (排序查询)
image ← S3 (上传), user (每日上传限制)
```

因此 message 模块的**写入侧** (创建消息) 实际上在 Phase 2 和 Phase 3 实现 galgame/topic 时就需要。建议:
- Phase 2/3 时先实现 `messageRepository.Create(msg)` 作为内部依赖
- Phase 4 实现 message 模块的读取端点
