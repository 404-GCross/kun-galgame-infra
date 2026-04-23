# 02 — 存储与 Schema 设计

## 对象存储布局

### Bucket & Key

- **Bucket**：`kun-images`（生产）/ `kun-images-dev`（开发）/ `kun-images-test`（测试）
- **Key 格式**：
  ```
  <site>/<hash[:2]>/<hash[2:4]>/<hash>.<ext>
  ```
- **示例**：
  ```
  kungal/ab/cd/abcd1234567890abcdef...ef.webp
  moyu/12/34/1234abcdef1234567890...ff.webp
  galgame_wiki/56/78/5678abc123...aa.jpg
  ```

### 设计说明

| 层 | 作用 |
|----|------|
| `<site>` | 便于按调用方统计、清理、迁移；生命周期策略可按站点独立 |
| `<hash[:2]>/<hash[2:4]>` | 前缀分片。对象存储本身无目录概念，但便于 `aws s3 ls` / `rclone` 扫描 |
| `<hash>.<ext>` | SHA-256 全 hash + 原始扩展名。扩展名只做信息用，真实类型以元数据 `mime` 为准 |

### 原图 vs 派生图

- **上传产物**：`<site>/<hash>/<hash>.<ext>`（单份，压缩 or 原图按站点配置）
- **派生图**：**不存**，imgproxy 按需从上面这份生成 + CDN 缓存

### 生命周期策略

对象存储配置（S3 Lifecycle / R2 Lifecycle）：

| 规则 | 触发条件 | 动作 |
|------|---------|------|
| Standard → IA | 上次访问 > 60 天 | 转低频存储 |
| IA → Archive | 上次访问 > 180 天 | 转归档存储（R2 没这层可跳过） |
| 软删 → 物理删 | `deleted_at < now - 30d` | 物理删除 |

> 对象存储的 "last access" 通常要开启 Intelligent Tiering 才有；如果没开，可改用 `last_modified`（但不准）或由图片服务的清理 worker 显式转存储层。

## PostgreSQL Schema

### 复用哪个库？

图片服务**独立建库**：`kun_images`。理由：
- 不污染 `kun_oauth_admin` / `kun_galgame_wiki` 现有 schema
- 故障隔离（图片服务 DB 挂了不影响 OAuth）
- 容量伸缩独立

连接配置新增：
```env
KUN_IMAGES_PG_HOST=localhost
KUN_IMAGES_PG_PORT=5432
KUN_IMAGES_PG_USER=postgres
KUN_IMAGES_PG_PASSWORD=...
KUN_IMAGES_PG_DATABASE=kun_images
```

### 表结构

#### `images` — 核心表

```sql
CREATE TABLE images (
    id              BIGSERIAL PRIMARY KEY,
    hash            CHAR(64) NOT NULL,           -- sha256 hex
    site            VARCHAR(32) NOT NULL,        -- 首次上传来源站
    storage_key     VARCHAR(512) NOT NULL,       -- 对象存储 key
    mime            VARCHAR(32) NOT NULL,        -- image/webp / image/jpeg / ...
    ext             VARCHAR(8) NOT NULL,         -- webp / jpg / png

    width           INTEGER NOT NULL,
    height          INTEGER NOT NULL,
    size_bytes      BIGINT NOT NULL,

    is_original     BOOLEAN NOT NULL DEFAULT FALSE,  -- 是否保留了原图（站点开关）
    origin_mime     VARCHAR(32),                 -- 原始格式（转码前）
    origin_size     BIGINT,                      -- 原始大小（转码前）

    review_status   SMALLINT NOT NULL DEFAULT 0, -- 0 待审 / 1 通过 / 2 拒绝 / 3 人工复核
    review_labels   JSONB,                       -- AI 审核标签明细
    reviewed_at     TIMESTAMPTZ,

    uploader_sub    VARCHAR(64),                 -- 上传者 OAuth sub（user id）
    uploader_client VARCHAR(64),                 -- 上传者 OAuth client_id
    uploader_ip     INET,

    last_referenced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- 最后被 ping 时间
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,                 -- 软删时间戳

    CONSTRAINT images_hash_site_uniq UNIQUE (hash, site)
);

CREATE INDEX idx_images_hash ON images(hash);
CREATE INDEX idx_images_site_created ON images(site, created_at DESC);
CREATE INDEX idx_images_review_status ON images(review_status) WHERE review_status IN (0, 3);
CREATE INDEX idx_images_last_ref ON images(last_referenced_at) WHERE deleted_at IS NULL;
CREATE INDEX idx_images_deleted ON images(deleted_at) WHERE deleted_at IS NOT NULL;
```

**为什么 `UNIQUE (hash, site)` 而不是 `UNIQUE (hash)`**：
- 同一张图被 kungal 和 moyu 分别上传时，物理上只存一份没问题，但：
  - 审计视角：各站要独立统计上传量
  - 审核视角：某站对这张图的审核结果可能不同（如 moyu 宽松 / kungal 严格）
  - 清理视角：各站独立 ping `last_referenced_at`
- 存储层可以做软链（两条 `images` 行指向同一个 `storage_key`），见"去重策略"

**去重策略**：
- 上传时先查 `SELECT id FROM images WHERE hash = ? LIMIT 1`
- 命中：复用现有 `storage_key`，**只插新的 `images` 行**（不重新上传对象）
- 未命中：处理 + 上传 + 插入

#### `upload_audit` — 上传审计（可选，小量可合并入日志）

```sql
CREATE TABLE upload_audit (
    id              BIGSERIAL PRIMARY KEY,
    image_id        BIGINT REFERENCES images(id) ON DELETE SET NULL,
    hash            CHAR(64) NOT NULL,
    site            VARCHAR(32) NOT NULL,
    uploader_sub    VARCHAR(64),
    uploader_client VARCHAR(64),
    uploader_ip     INET,
    preset          VARCHAR(32),
    result          VARCHAR(16) NOT NULL,        -- success / rejected_moderation / rejected_quota / error
    error_reason    TEXT,
    duration_ms     INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_site_created ON upload_audit(site, created_at DESC);
CREATE INDEX idx_audit_uploader ON upload_audit(uploader_sub, created_at DESC);
```

#### `reference_pings` — 引用续期（可选）

如果调用方采用"周期性批量 ping"方案（见决策 4），图片服务接收端点 `POST /image/reference-ping`，可以直接 `UPDATE images SET last_referenced_at = NOW() WHERE hash = ANY($1)`，无需额外表。

但如果想追踪"哪个站的哪次 ping 续期了哪些图"，可建：

```sql
CREATE TABLE reference_pings (
    id              BIGSERIAL PRIMARY KEY,
    site            VARCHAR(32) NOT NULL,
    ping_batch_id   VARCHAR(64),                 -- 调用方自定义批次 ID
    hashes_count    INTEGER NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

M1 阶段可以**先不做这张表**，观察需要再加。

## 站点配置扩展 OAuth Client

新增字段（在 `kun_oauth_admin.oauth_client` 表上 `ALTER TABLE`）：

```sql
ALTER TABLE oauth_client
    ADD COLUMN image_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN image_quota_daily INTEGER DEFAULT 10000,         -- 每日上传张数
    ADD COLUMN image_quota_bytes_daily BIGINT DEFAULT 10737418240,  -- 每日上传字节数（10GB）
    ADD COLUMN image_max_file_size BIGINT DEFAULT 10485760,     -- 单文件上限（10MB）
    ADD COLUMN image_allow_original BOOLEAN NOT NULL DEFAULT FALSE,  -- 是否允许保留原图
    ADD COLUMN image_allowed_presets TEXT[] DEFAULT ARRAY['cover', 'thumbnail'],
    ADD COLUMN image_site_key VARCHAR(32);                      -- 对象存储 key 前缀（如 'kungal'）
```

### 新站点接入流程

1. 在 `oauth_client` 表 `INSERT` 一行，填 `image_site_key='new_site'`、`image_enabled=true` 等
2. 申请 OAuth Client 的 scope 时包含 `image:upload`
3. 调用方代码里用新 `client_id` / `client_secret` 换 access_token
4. 直接调图片服务 API —— **零代码改动到图片服务侧**

## Preset 配置（服务端配置文件）

放在 `apps/api/configs/image_presets.yaml`：

```yaml
presets:
  # 通用
  cover:
    max_width: 1920
    max_height: 1080
    quality: 82
    format: webp
    strip_exif: true

  thumbnail:
    max_width: 400
    max_height: 400
    quality: 80
    format: webp
    strip_exif: true

  avatar:
    max_width: 256
    max_height: 256
    quality: 85
    format: webp
    strip_exif: true

  # kungal topic 图（保留长尺寸）
  topic_image:
    max_width: 1600
    max_height: 4000         # 长截图友好
    quality: 82
    format: webp
    strip_exif: true

  # galgame 保留原图
  galgame_banner:
    max_width: 0             # 0 = 不缩放
    max_height: 0
    quality: 0               # 0 = 不转码
    format: original         # 保留原格式
    strip_exif: true
    max_file_size: 52428800  # 50MB
```

## imgproxy URL 格式

派生尺寸走 imgproxy，URL 形如：

```
https://img.cdn.example.com/<signature>/rs:fit:<w>:<h>/<extension>/plain/s3://kun-images/<key>@<format>
```

实际例子：
```
https://img.cdn/abcd1234/rs:fit:400:400/webp/plain/s3://kun-images/kungal/ab/cd/abcd...ef.webp@webp
```

- `<signature>` 用 HMAC-SHA256 签名整个路径，防止外部乱拼参数
- 签名 key 存环境变量 `IMGPROXY_KEY` / `IMGPROXY_SALT`
- 后端提供工具函数 `BuildVariantURL(hash, site, ext, preset)` 供调用方使用，不暴露裸拼接

## 对外 URL 形态

调用方最终拿到两种 URL：

| 用途 | URL | 访问路径 |
|------|-----|---------|
| **原图/标准图** | `https://cdn.example.com/img/<site>/<hash>.<ext>` | CDN → 回源到对象存储 |
| **指定变体** | `https://img.cdn.example.com/<signature>/rs:fit:W:H/webp/plain/...` | CDN → imgproxy → 对象存储 |

CDN 层面做 URL 美化（第一种），imgproxy 走独立子域名（第二种）。

下一篇：[03 — API 设计](./03-api-design.md)
