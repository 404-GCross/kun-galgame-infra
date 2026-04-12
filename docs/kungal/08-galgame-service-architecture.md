# Galgame Service 架构设计

> 最终确认版 — 2026-04-12

## 定位

独立的 galgame 元数据服务，部署在 OAuth repo 的 `cmd/galgame/main.go`，为 kungal、moyu 及未来站点提供 galgame 信息的统一 CRUD。

## 核心决策总结

| 决策项 | 结论 |
|--------|------|
| user_id | 整数，全局一致（三库已同步），无需改 |
| count 列 | 从 galgame 表移除，各站自维护 |
| galgame_contributor | 归 galgame service（后续升级） |
| 数据库 | 独立库 `kun_galgame_wiki`，同时只读连接 `kun_oauth_admin` 查用户信息 |
| S3 | 共享 bucket，galgame service 专属 |
| moyu | 只读 galgame 元数据 |
| API 认证 | 读操作公开，写操作需 OAuth Bearer Token |
| VNDB/DLSite/Bangumi | galgame service 全权管理，全量拉取聚合 |
| VNDB 同步失败 | 不影响创建，异步同步 |

---

## 数据库拓扑

```
┌─────────────────────┐
│   kun_oauth_admin    │ ← OAuth 主库
│   (PostgreSQL)       │
│                      │
│   users              │ ← galgame service 只读连接
│   user_site_data     │
│   user_roles         │
│   ...                │
└──────────┬──────────┘
           │ 只读
           │
┌──────────▼──────────┐       ┌───────────────────┐
│  kun_galgame_wiki   │       │  kungal 主库       │
│  (PostgreSQL)        │       │  (PostgreSQL)      │
│                      │       │                    │
│  galgame (14 表)     │       │  galgame_like      │
│  + contributor       │       │  galgame_favorite   │
│  + vndb/dlsite 缓存  │       │  galgame_comment    │
│                      │       │  galgame_rating     │
└──────────────────────┘       │  galgame_resource   │
                               │  galgame_stats      │ ← 新表
                               │  topic, message...  │
                               └───────────────────┘

                               ┌───────────────────┐
                               │  moyu 主库         │
                               │  (PostgreSQL)      │
                               │                    │
                               │  patch             │
                               │  patch_resource    │
                               │  ...               │
                               └───────────────────┘
```

### galgame service 双数据库连接

```go
type App struct {
    WikiDB  *gorm.DB  // kun_galgame_wiki — 读写
    OAuthDB *gorm.DB  // kun_oauth_admin  — 只读
}

// 查用户信息时用 OAuthDB
func (r *UserReader) GetUserBrief(uid int) (*UserBrief, error) {
    var user UserBrief
    err := r.oauthDB.Table("users").
        Select("id, name, avatar").
        Where("id = ?", uid).
        First(&user).Error
    return &user, err
}
```

---

## 表归属

### galgame service 拥有 — `kun_galgame_wiki` 库（15 张表）

| 表 | 说明 |
|---|---|
| `galgame` | 核心记录（移除 6 个 count 列） |
| `galgame_series` | 系列 |
| `galgame_alias` | 别名 |
| `galgame_tag` | 标签定义 |
| `galgame_tag_alias` | 标签别名 |
| `galgame_tag_relation` | galgame↔tag |
| `galgame_official` | 开发商/发行商 |
| `galgame_official_alias` | 开发商别名 |
| `galgame_official_relation` | galgame↔official |
| `galgame_engine` | 引擎 |
| `galgame_engine_relation` | galgame↔engine |
| `galgame_link` | 外部链接 |
| `galgame_pr` | 编辑请求 |
| `galgame_history` | 编辑历史 |
| `galgame_contributor` | 贡献者（后续升级） |

### `galgame` 表列变更

```sql
-- 保留
id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw,
banner, intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw,
content_limit, status, view, original_language, age_limit,
user_id, series_id, resource_update_time, created, updated

-- 移除（迁移到各站 galgame_stats）
-- like_count
-- favorite_count
-- resource_count
-- comment_count
-- contributor_count  ← contributor 归 galgame service，但 count 可从表聚合
-- rating_count
```

注：`contributor_count` 可以保留在 galgame 表（因为 contributor 归 galgame service），也可以直接 `COUNT(*)` 查询。按当前数据量直接查询即可，不需要冗余列。

### 各站点后端拥有 — 站点自己的库（以 kungal 为例，12 + 1 张表）

| 表 | 说明 |
|---|---|
| `galgame_like` | 点赞 |
| `galgame_favorite` | 收藏 |
| `galgame_comment` | 评论 |
| `galgame_comment_like` | 评论点赞 |
| `galgame_rating` | 评分 |
| `galgame_rating_like` | 评分点赞 |
| `galgame_rating_comment` | 评分评论 |
| `galgame_resource` | 下载资源 |
| `galgame_resource_provider` | 资源 provider |
| `galgame_resource_link` | 资源链接 |
| `galgame_resource_like` | 资源点赞 |
| `galgame_stats` | **新表**，站点级聚合计数 |

### `galgame_stats` 新表（各站各一份）

```sql
CREATE TABLE galgame_stats (
    galgame_id INT PRIMARY KEY,
    like_count INT NOT NULL DEFAULT 0,
    favorite_count INT NOT NULL DEFAULT 0,
    resource_count INT NOT NULL DEFAULT 0,
    comment_count INT NOT NULL DEFAULT 0,
    rating_count INT NOT NULL DEFAULT 0
);
```

站点后端在 like/comment/resource 等操作的事务中同步维护。

---

## API 设计

### 认证

- **读操作（GET）**：公开，无需认证
- **写操作（POST/PUT/DELETE）**：需要 OAuth Bearer Token
- galgame service 验证 JWT → 提取 `sub` (UUID) → 查 `kun_oauth_admin.users` 获取 integer `user_id`

### 端点清单

```
# ── 核心 CRUD ──
GET    /api/galgame                    # 列表（分页+筛选）
GET    /api/galgame/:gid              # 详情（含全部关联）
POST   /api/galgame                    # 创建 [需认证]
PUT    /api/galgame/:gid              # 直接更新（创建者 or admin）[需认证]
DELETE /api/galgame/:gid              # 删除（admin）[需认证]
GET    /api/galgame/check              # VNDB ID 存在性检查

# ── 别名 ──
POST   /api/galgame/:gid/alias        # [需认证]
DELETE /api/galgame/:gid/alias/:id     # [需认证]

# ── 外部链接 ──
GET    /api/galgame/:gid/link
POST   /api/galgame/:gid/link          # [需认证]
DELETE /api/galgame/:gid/link/:id       # [需认证]

# ── PR（编辑请求）──
GET    /api/galgame/:gid/pr
POST   /api/galgame/:gid/pr            # 提交修改请求 [需认证]
PUT    /api/galgame/:gid/pr/:id/merge   # 合并（创建者 or admin）[需认证]
PUT    /api/galgame/:gid/pr/:id/decline # 拒绝 [需认证]

# ── 历史 ──
GET    /api/galgame/:gid/history

# ── 贡献者 ──
GET    /api/galgame/:gid/contributor
POST   /api/galgame/:gid/contributor    # [需认证]
DELETE /api/galgame/:gid/contributor/:uid # [需认证]

# ── 元数据 CRUD ──
GET/POST/PUT/DELETE  /api/tag
GET/POST/PUT/DELETE  /api/official
GET/POST/PUT/DELETE  /api/engine
GET/POST/PUT/DELETE  /api/series

# ── 数据源同步 ──
POST   /api/galgame/:gid/sync          # 触发 VNDB/DLSite/Bangumi 重新同步 [需认证]
GET    /api/sync/status                 # 全量同步状态
```

### 响应格式

与 kungal 后端一致：

```json
{
  "code": 0,
  "message": "成功",
  "data": { ... }
}
```

---

## 站点后端调用模式

以 kungal 后端为例：

### 读取 — 聚合两个数据源

```go
func (h *GalgameHandler) GetDetail(c *fiber.Ctx) error {
    gid := c.Params("gid")
    uid := getOptionalUserID(c)

    // 并行查询
    g, ctx := errgroup.WithContext(c.Context())

    var meta *GalgameDetail         // 从 galgame service
    var stats *GalgameStats         // 从本地 galgame_stats
    var interaction *UserInteraction // 从本地 like/favorite 表

    g.Go(func() error {
        var err error
        meta, err = h.galgameClient.GetDetail(ctx, gid)
        return err
    })
    g.Go(func() error {
        var err error
        stats, err = h.localRepo.GetStats(ctx, gid)
        return err
    })
    if uid > 0 {
        g.Go(func() error {
            var err error
            interaction, err = h.localRepo.GetUserInteraction(ctx, gid, uid)
            return err
        })
    }

    if err := g.Wait(); err != nil {
        return response.Error(c, errors.ErrInternal("获取 Galgame 详情失败"))
    }

    // 合并返回
    return response.OK(c, mergeDetailResponse(meta, stats, interaction))
}
```

### 写入 — 调 galgame service + 本地副作用

```go
func (h *GalgameHandler) Create(c *fiber.Ctx) error {
    user, _ := middleware.MustGetUser(c)

    // 1. 转发到 galgame service（galgame service 处理元数据+VNDB+S3）
    result, err := h.galgameClient.Create(ctx, oauthAccessToken, body)
    if err != nil {
        return response.Error(c, errors.ErrBadRequest("创建失败"))
    }

    // 2. 本地副作用（事务）
    h.db.Transaction(func(tx *gorm.DB) error {
        // 初始化 stats 记录
        tx.Create(&GalgameStats{GalgameID: result.ID})
        // 萌萌点 +3
        tx.Model(&User{}).Where("id = ?", user.UID).
            Update("moemoepoint", gorm.Expr("moemoepoint + 3"))
        return nil
    })

    return response.OK(c, result)
}
```

### 互动 — 纯本地操作

```go
func (h *GalgameHandler) ToggleLike(c *fiber.Ctx) error {
    // 不需要调 galgame service，直接操作本地表
    // galgame_like + galgame_stats.like_count
}
```

---

## 数据迁移计划

从 kungal 现有数据库迁移到 `kun_galgame_wiki`：

### 需要迁移的表（15 张）

```sql
-- 完整迁移（含数据）
galgame, galgame_series, galgame_alias,
galgame_tag, galgame_tag_alias, galgame_tag_relation,
galgame_official, galgame_official_alias, galgame_official_relation,
galgame_engine, galgame_engine_relation,
galgame_link, galgame_pr, galgame_history,
galgame_contributor
```

### 迁移后 kungal 库的处理

```sql
-- kungal 库中：
-- 1. 从 galgame 表移除 count 列
ALTER TABLE galgame DROP COLUMN like_count;
ALTER TABLE galgame DROP COLUMN favorite_count;
ALTER TABLE galgame DROP COLUMN resource_count;
ALTER TABLE galgame DROP COLUMN comment_count;
ALTER TABLE galgame DROP COLUMN contributor_count;
ALTER TABLE galgame DROP COLUMN rating_count;

-- 2. 创建 galgame_stats 表并回填
CREATE TABLE galgame_stats AS
SELECT
    g.id AS galgame_id,
    (SELECT COUNT(*) FROM galgame_like WHERE galgame_id = g.id) AS like_count,
    (SELECT COUNT(*) FROM galgame_favorite WHERE galgame_id = g.id) AS favorite_count,
    (SELECT COUNT(*) FROM galgame_resource WHERE galgame_id = g.id) AS resource_count,
    (SELECT COUNT(*) FROM galgame_comment WHERE galgame_id = g.id) AS comment_count,
    (SELECT COUNT(*) FROM galgame_rating WHERE galgame_id = g.id) AS rating_count
FROM galgame g;

ALTER TABLE galgame_stats ADD PRIMARY KEY (galgame_id);

-- 3. 迁移后可以删除 kungal 库中的 15 张元数据表（或保留只读作为备份）
-- 建议: 保留 3 个月后再删
```

### 迁移后 `kun_galgame_wiki` 的 galgame 表

```sql
-- 不含 count 列
CREATE TABLE galgame (
    id SERIAL PRIMARY KEY,
    vndb_id VARCHAR(10) UNIQUE,
    name_en_us VARCHAR(1000) DEFAULT '',
    name_ja_jp VARCHAR(1000) DEFAULT '',
    name_zh_cn VARCHAR(1000) DEFAULT '',
    name_zh_tw VARCHAR(1000) DEFAULT '',
    banner VARCHAR(233) DEFAULT '',
    intro_en_us TEXT DEFAULT '',
    intro_ja_jp TEXT DEFAULT '',
    intro_zh_cn TEXT DEFAULT '',
    intro_zh_tw TEXT DEFAULT '',
    content_limit VARCHAR(10) DEFAULT 'sfw',
    status INT DEFAULT 0,
    view INT DEFAULT 0,
    resource_update_time TIMESTAMP DEFAULT NOW(),
    original_language VARCHAR(10) DEFAULT 'ja-jp',
    age_limit VARCHAR(10) DEFAULT 'r18',
    user_id INT NOT NULL,
    series_id INT,
    created TIMESTAMP DEFAULT NOW(),
    updated TIMESTAMP DEFAULT NOW()
);
```

---

## VNDB/DLSite/Bangumi 数据聚合

galgame service 全权管理外部数据源同步：

```
kun_galgame_wiki 库新增表（未来）:

vndb_cache           ← VNDB 全量视觉小说缓存
dlsite_cache         ← DLSite 数据缓存
bangumi_cache        ← Bangumi 数据缓存
sync_job             ← 同步任务状态追踪
```

同步策略：
- 全量拉取 VNDB（~5 万条），定期增量更新
- 创建 galgame 时自动关联 VNDB 数据（tag/developer/engine）
- 同步失败不阻塞 galgame 创建，后台异步重试

---

## 对 kungal 后端 (`apps/api`) 的改动

### 新增

```
internal/galgame/client/       ← galgame service HTTP client
internal/galgame/client/client.go
internal/galgame/client/types.go
```

### 修改

```
internal/app/app.go            ← 注入 GalgameClient
internal/app/router.go         ← galgame 路由注册
internal/galgame/handler/      ← 从"直接操作DB"改为"调 client + 本地副作用"
internal/galgame/repository/   ← 只操作本地交互表 + galgame_stats
internal/galgame/model/        ← 移除已迁走的 14 张表模型，保留交互表模型
```

### 删除

```
internal/galgame/model/ 中的元数据模型（已迁到 galgame service）
```

---

## 实施顺序

1. 在 OAuth repo 创建 `cmd/galgame/main.go` + 基础框架
2. 定义 `kun_galgame_wiki` 数据库 schema
3. 实现 galgame 核心 CRUD（不含 VNDB 同步）
4. 实现元数据 CRUD（tag/official/engine/series）
5. 实现 PR + history + contributor
6. 实现 link + alias
7. 编写数据迁移脚本（kungal → kun_galgame_wiki）
8. 在 kungal 后端实现 GalgameClient + 聚合层
9. 迁移 kungal 后端的交互端点（like/comment/rating/resource）使用 galgame_stats
10. 集成 VNDB/DLSite/Bangumi 全量同步
