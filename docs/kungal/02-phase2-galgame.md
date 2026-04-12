# Phase 2: Galgame 核心模块实现计划

> **⚠️ 已过时** — 本文档在 galgame service 独立方案确定之前编写。
> 最终架构见 `08-galgame-service-architecture.md`。
>
> 端点拆分：
> - **Step 1/5/6/8/9**（CRUD/PR/link/series/元数据）→ galgame service (OAuth repo)
> - **Step 2/3/4/7**（like/comment/resource/rating）→ kungal 后端 (apps/api)
>
> 以下内容仅作为 Nitro 业务逻辑参考，目录结构和实现位置已不适用。

> 预计端点数: ~55 | 前置: Phase 1 (已完成)

## 模块结构

```
internal/galgame/
├── model/galgame.go          ✅ 已定义 (434 行, 30+ 模型)
├── dto/
│   ├── galgame_dto.go        ❌ 待创建
│   ├── comment_dto.go        ❌
│   ├── resource_dto.go       ❌
│   ├── pr_dto.go             ❌
│   ├── rating_dto.go         ❌
│   └── series_dto.go         ❌
├── repository/
│   ├── galgame_repo.go       ❌
│   ├── comment_repo.go       ❌
│   ├── resource_repo.go      ❌
│   ├── pr_repo.go            ❌
│   ├── rating_repo.go        ❌
│   └── series_repo.go        ❌
├── service/
│   ├── galgame_service.go    ❌
│   ├── comment_service.go    ❌
│   ├── resource_service.go   ❌
│   ├── pr_service.go         ❌
│   ├── rating_service.go     ❌
│   └── series_service.go     ❌
└── handler/
    ├── galgame_handler.go    ❌
    ├── comment_handler.go    ❌
    ├── resource_handler.go   ❌
    ├── pr_handler.go         ❌
    ├── rating_handler.go     ❌
    └── series_handler.go     ❌
```

## 实现顺序

### Step 1: Galgame 基础 CRUD (5 端点)

| HTTP | 路径 | 说明 | Nitro 文件 |
|------|------|------|-----------|
| GET | /api/galgame | 列表 (分页+筛选) | api/galgame/index.get.ts |
| GET | /api/galgame/:gid | 详情 (10+ 关联) | api/galgame/[gid]/index.get.ts |
| POST | /api/galgame | 创建 (事务+萌萌点+3) | api/galgame/index.post.ts |
| PUT | /api/galgame/:gid | 更新 | 通过 PR 系统 |
| GET | /api/galgame/check | 存在性检查 | api/galgame/check.get.ts |

**列表端点关键逻辑:**
- 筛选: type(全部/按语言/按平台), sortField(created/view/like_count), sortOrder
- NSFW 过滤: content_limit != 'nsfw' (如果用户无 NSFW cookie)
- 分页: page + limit
- 返回: galgame 卡片 (多语言名称, banner, 各种 count, 标签, 开发商)

**详情端点关键逻辑:**
- 主记录 + 所有关联数据:
  - GalgameAlias (别名列表)
  - GalgameTagRelation → GalgameTag (标签)
  - GalgameOfficialRelation → GalgameOfficial (开发商)
  - GalgameEngineRelation → GalgameEngine (引擎)
  - GalgameSeries (所属系列)
  - GalgameContributor (贡献者列表, 含用户名/头像)
  - 如果已登录: 当前用户是否已点赞/收藏/是否是贡献者
- 浏览量 +1 (异步, 不阻塞)

**创建端点关键逻辑:**
- 认证 + 萌萌点检查
- 验证 vndb_id 格式和唯一性
- 事务:
  1. 创建 Galgame 主记录
  2. 创建 aliases (GalgameAlias)
  3. 创建 tag/official/engine 关联
  4. 添加创建者为贡献者
  5. 创建历史记录 (action=created)
  6. 用户萌萌点 +3

### Step 2: Galgame 互动 (3 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| PUT | /api/galgame/:gid/like | 点赞/取消 |
| PUT | /api/galgame/:gid/favorite | 收藏/取消 |
| PUT | /api/galgame/:gid/contributor | 贡献者管理 |

**Toggle 模式:** 查找已有 → 存在则删除 + count-1, 不存在则创建 + count+1

### Step 3: Galgame Comment (4 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/galgame/:gid/comment | 评论列表 |
| POST | /api/galgame/:gid/comment | 发表评论 |
| PUT | /api/galgame/:gid/comment/like | 评论点赞 |
| DELETE | /api/galgame/:gid/comment | 删除评论 |

**关键:** 评论含 target_user_id (回复某人), 需创建消息通知

### Step 4: Galgame Resource (8 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/galgame/:gid/resource | 资源列表 |
| POST | /api/galgame/:gid/resource | 添加资源 (+3 萌萌点) |
| PUT | /api/galgame/:gid/resource | 编辑资源 |
| DELETE | /api/galgame/:gid/resource | 删除资源 |
| PUT | /api/galgame/:gid/resource/like | 资源点赞 |
| PUT | /api/galgame/:gid/resource/valid | 标记有效 |
| PUT | /api/galgame/:gid/resource/expired | 标记过期 |
| GET | /api/galgame-resource (全局) | 资源浏览 (4 端点) |

**关键逻辑:**
- 资源含 provider 检测 (从 URL 自动识别下载站)
- GalgameResourceProvider 关联表 (替代原 text[])
- GalgameResourceLink 关联表
- 创建时: 贡献者列表更新 + resource_count +1 + 萌萌点 +3
- 资源 like 计数维护

### Step 5: Galgame PR + History (6 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/galgame/:gid/pr | PR 列表 |
| GET | /api/galgame/:gid/pr/all | 全部 PR |
| POST | /api/galgame/:gid/pr | 提交 PR |
| PUT | /api/galgame/:gid/pr/merge | 合并 PR |
| PUT | /api/galgame/:gid/pr/decline | 拒绝 PR |
| GET | /api/galgame/:gid/history | 编辑历史 |

**PR 工作流 (核心复杂逻辑):**

创建 PR:
```
if 用户是 galgame 创建者 or role >= 3:
    直接更新 galgame 记录
    创建历史 (action=updated)
else:
    创建 GalgamePR (status=0, old_data=当前值, new_data=新值)
    创建消息通知 galgame 创建者 (type=requested)
```

合并 PR:
```
事务:
1. 校验 PR 存在且 status=0
2. 校验操作者是 galgame 创建者 or role >= 3
3. 应用 new_data 到 galgame 记录
4. 更新 PR status=1, completed_time
5. 如果 vndb_id 变更 → 重新同步 VNDB
6. 添加 PR 提交者为贡献者
7. PR 提交者萌萌点 +1
8. 创建历史 (action=merged)
9. 创建消息通知 PR 提交者 (type=merged)
```

### Step 6: Galgame Link (3 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/galgame/:gid/link | 外部链接列表 |
| POST | /api/galgame/:gid/link | 添加链接 |
| DELETE | /api/galgame/:gid/link | 删除链接 |

简单 CRUD, 无复杂逻辑

### Step 7: Galgame Rating (9 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/galgame-rating/all | 评分列表 |
| POST | /api/galgame-rating | 创建评分 |
| GET | /api/galgame-rating/:id | 评分详情 |
| PUT | /api/galgame-rating/:id | 编辑评分 |
| DELETE | /api/galgame-rating/:id | 删除评分 |
| PUT | /api/galgame-rating/:id/like | 评分点赞 |
| GET | /api/galgame-rating/:id/comment | 评分评论列表 |
| POST | /api/galgame-rating/:id/comment | 发表评论 |
| DELETE | /api/galgame-rating/:id/comment | 删除评论 |

**萌萌点规则:**
- short_summary 长度 < 233: +3
- 233 ≤ 长度 < 666: +5
- 长度 ≥ 666: +10

### Step 8: Galgame Series (7 端点)

| HTTP | 路径 | 说明 |
|------|------|------|
| GET | /api/galgame-series | 系列列表 |
| POST | /api/galgame-series | 创建系列 |
| GET | /api/galgame-series/search | 搜索系列 |
| POST | /api/galgame-series/modal | 模态创建 |
| GET | /api/galgame-series/:id | 系列详情 |
| PUT | /api/galgame-series/:id | 编辑系列 |
| DELETE | /api/galgame-series/:id | 删除系列 |

### Step 9: Galgame 元数据 (12 端点)

**Tag (5 端点):**
- GET /api/galgame-tag — 列表
- GET /api/galgame-tag/:name — 按名查询
- GET /api/galgame-tag/search — 搜索
- GET /api/galgame-tag/multi — 批量查询
- PUT /api/galgame-tag — 更新

**Engine (3 端点):**
- GET /api/galgame-engine — 列表
- GET /api/galgame-engine/:name — 按名查询
- PUT /api/galgame-engine — 更新

**Official (4 端点):**
- GET /api/galgame-official — 列表
- GET /api/galgame-official/:name — 按名查询
- GET /api/galgame-official/search — 搜索
- PUT /api/galgame-official — 更新

## 关键 Nitro 文件参考

实现时需要参考的 Nitro 源码 (位于 `apps/nitro-server/api/`):

```
galgame/
├── index.get.ts          → 列表筛选逻辑
├── index.post.ts         → 创建事务 (最复杂)
├── check.get.ts          → 存在性检查
└── [gid]/
    ├── index.get.ts      → 详情查询 (10+ include)
    ├── banner.put.ts     → Banner 上传
    ├── like.put.ts       → 点赞 toggle
    ├── favorite.put.ts   → 收藏 toggle
    ├── contributor.put.ts
    ├── comment/          → 评论 CRUD
    ├── resource/         → 资源管理 (7 文件)
    ├── pr/               → PR 工作流 (5 文件)
    ├── link/             → 外部链接
    └── history/          → 编辑历史
```

## 预估工作量

| Step | 端点数 | 复杂度 | 关键难点 |
|------|--------|--------|---------|
| 1. 基础 CRUD | 5 | ⭐⭐⭐⭐ | 详情页 10+ 关联查询, 创建事务 |
| 2. 互动 | 3 | ⭐⭐ | Toggle 模式 + count 维护 |
| 3. 评论 | 4 | ⭐⭐ | 消息通知 |
| 4. 资源 | 8 | ⭐⭐⭐ | Provider 检测, 多关联表 |
| 5. PR | 6 | ⭐⭐⭐⭐⭐ | 最复杂: diff 存储, merge 逻辑 |
| 6. 链接 | 3 | ⭐ | 简单 CRUD |
| 7. 评分 | 9 | ⭐⭐⭐ | 8 维度评分, 萌萌点分级 |
| 8. 系列 | 7 | ⭐⭐ | 标准 CRUD |
| 9. 元数据 | 12 | ⭐⭐ | 搜索 + 别名管理 |
