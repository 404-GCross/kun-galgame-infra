# 05 — 工程计划

## 里程碑概览

| 里程碑 | 目标 | 预估 | 依赖 |
|--------|------|------|------|
| **M1** | 骨架打通：上传→处理→存储→返回 URL | 3–4 天 | 无 |
| **M2** | 鉴权 + 站点配置 + 配额 | 2–3 天 | M1 |
| **M3** | imgproxy + CDN + 变体 URL + 压测 | 2–3 天 | M2 |
| **M4** | 审核（同步 + 异步）+ 管理后台 | 3–4 天 | M3 |
| **M5** | 迁移脚本 + 旧系统切换 | 按站点推进，2 周观察 | M4 |

**整体周期**：约 2.5–3 周工程 + 2 周灰度/迁移观察。

---

## M1 — MVP 骨架

### 目标

端到端能跑通：curl 上传一张 PNG，图片服务转成 WebP、存到 MinIO、返回 URL，浏览器能访问。

### 交付物

#### 代码

```
apps/api/
  cmd/
    image/                        # 新增：图片服务进程入口
      main.go
  configs/
    image_presets.yaml            # 预设配置
  internal/
    infrastructure/
      storage/                    # 新增：S3 抽象
        client.go
        s3.go
    platform/
      image/                      # 新增：图片服务业务层
        handler/
          upload.go
        service/
          service.go              # 上传主流程
          processor.go            # 调 libvips 的薄封装
          dedup.go                # hash 查重
        model/
          image.go                # GORM model
        repository/
          image_repo.go
  pkg/
    config/
      image.go                    # 图片服务配置结构
migrations/
  image/
    001_create_images.sql
```

#### 功能

- `POST /image/upload` 接 multipart，返回 hash + URL
- MIME 嗅探（`gabriel-vasile/mimetype`）
- libvips 解码 → resize → 编码 WebP（用 `govips`）
- 按 preset 配置处理（写死 3 个：`cover` / `thumbnail` / `avatar`）
- 对象存储用 MinIO（本地开发）/ S3（生产）
- hash 去重（同 hash 不重传）
- `images` 表写入
- `GET /healthz`

#### 不做

- ❌ 鉴权（先裸接口方便开发，M2 补）
- ❌ 配额
- ❌ 审核
- ❌ imgproxy
- ❌ 管理端点

### 验收标准

- [ ] `curl -F file=@test.png -F preset=cover http://localhost:9278/image/upload` 成功返回 hash + URL
- [ ] 返回的 URL 可以在浏览器直接打开看到图片
- [ ] 同一张图再传，返回相同 hash，`deduplicated: true`
- [ ] 10MB 以内的 PNG/JPG/WebP 都能处理
- [ ] 处理时间 < 500ms（单张 <5MB）
- [ ] 单元测试覆盖 processor / dedup / repo 核心路径

---

## M2 — 鉴权 + 配额

### 目标

图片服务接入 OAuth，只允许注册的 Client 上传；按站点做配额限制。

### 交付物

#### 代码

```
apps/api/
  internal/
    platform/
      image/
        handler/
          upload.go             # 加鉴权中间件
        middleware/
          auth.go               # OAuth token 校验 + scope + 站点解析
          quota.go              # 配额检查
        service/
          quota.go              # 配额消费（Redis 计数）
migrations/
  oauth/
    XXX_alter_oauth_client_add_image_fields.sql
```

#### 功能

- `oauth_client` 表 ALTER 加 `image_enabled` / `image_quota_daily` / `image_max_file_size` / `image_allow_original` / `image_allowed_presets` / `image_site_key` 字段
- 上传中间件：
  1. 解析 Bearer token → 获取 client_id（客户端凭证）或 sub+aud（用户 JWT）
  2. 从 `oauth_client` 取站点配置，拒绝未启用/未授权 scope
  3. 拒绝 preset 不在站点允许列表的请求
  4. 拒绝超过单文件大小的请求
- Redis 滑动窗口配额：
  - Key：`image:quota:{site}:{yyyymmdd}`
  - 每次上传前 INCR 并对比 `image_quota_daily`
  - 每次上传前累加 `image:quota:bytes:{site}:{yyyymmdd}` 对比 `image_quota_bytes_daily`
- `POST /image/reference-ping` 端点（简单 UPDATE `last_referenced_at`）

#### 不做

- ❌ 审核（下一阶段）
- ❌ imgproxy（下一阶段）

### 验收标准

- [ ] 无 token 请求返回 401
- [ ] token 缺 `image:upload` scope 返回 403
- [ ] 未启用图片服务的 Client 返回 403
- [ ] 超配额返回 429，带 `reset_at`
- [ ] 文件超过站点上限返回 413
- [ ] preset 不在允许列表返回 400
- [ ] 集成测试覆盖各拒绝路径

---

## M3 — imgproxy + CDN + 压测

### 目标

派生尺寸接通 imgproxy，调用方能拿到任意尺寸 URL；通过压测验证单机承载能力。

### 交付物

#### 部署

- 独立 imgproxy 容器（Docker Compose / K8s）
- Nginx/CDN 配置：
  - `cdn.example.com/img/*` → 对象存储（原图）
  - `img.cdn.example.com/*` → imgproxy

#### 代码

```
apps/api/
  pkg/
    imageclient/               # 新增：Go SDK
      variant.go               # BuildVariant 签名函数
      url.go
packages/
  image-client/                # 新增：TypeScript SDK
    src/
      buildVariant.ts
      index.ts
    package.json
```

#### 功能

- Go SDK `imageclient.BuildVariant(hash, site, ext, opts)` 返回签名 URL
- TS SDK `buildImageVariant(hash, opts)` 同上，用于前端
- 上传 API 的 `variants` 字段用 SDK 填充常用尺寸
- imgproxy HMAC key 管理（env + Vault 可选）

#### 压测

- `wrk` / `k6` 压上传端点
- 目标：单机 100 QPS 持续 5 分钟无报错，P99 < 800ms
- 记录 CPU/内存曲线，确定 semaphore 合理值

### 验收标准

- [ ] 上传返回的 `variants` URL 可以访问并拿到正确尺寸
- [ ] 直接访问 imgproxy 但签名错误 → 403
- [ ] 压测达标，相关指标采到 Prometheus
- [ ] SDK 在 kungal / moyu / galgame wiki 本地测试项目里能成功 import 使用

---

## M4 — 审核 + 管理后台

### 目标

接入 AI 审核，管理员能看到/处理违规图。

### 交付物

#### 代码

```
apps/api/
  internal/
    platform/
      image/
        moderation/
          sync.go              # 同步审核 Hook
          async.go             # 异步 worker
          provider/
            aliyun.go          # 阿里云内容安全
            tencent.go         # 腾讯云内容安全
            noop.go            # 占位实现
        handler/
          admin.go             # 管理端点
        service/
          admin.go
  cmd/
    image-moderation-worker/   # 独立 worker 进程
      main.go
apps/web/
  app/pages/
    image/
      list.vue                 # 管理列表
      detail/[hash].vue        # 详情 + 审核操作
```

#### 功能

- 同步审核：
  - 上传流程中在 store 前调用 `moderation.SyncCheck(ctx, bytes, meta)`
  - 超时（如 300ms）降级为 "pending"，不阻塞上传
  - 命中高置信度违规：返回 422，不写入 DB，不存对象
- 异步审核：
  - 上传成功后投递到队列（Postgres `image_moderation_queue` 表，SELECT FOR UPDATE SKIP LOCKED 消费）
  - Worker 并发调用审核 API，回填 `review_status` + `review_labels`
  - 拒绝的图更新 CDN/imgproxy 黑名单（通过 cache tag 刷新）
- 管理后台页面（复用 kun-oauth-admin 已有的 admin 壳）：
  - 待审列表（review_status = pending）
  - 已拒列表
  - 手动放行/拒绝
  - 按站点/上传者/时间过滤

### 验收标准

- [ ] 已知违规图（人为测试素材）同步拦截
- [ ] 异步 worker 消费队列，结果正确回填
- [ ] 管理员可以手动翻转 review 状态，状态变化触发 CDN 缓存刷新
- [ ] 审核故障时上传降级为 pending，系统不卡
- [ ] 监控：审核延迟、拒绝率、人工复核积压数

---

## M5 — 迁移 + 切换

按 [04-migration-plan.md](./04-migration-plan.md) 推进。

### 工程任务

- 编写 `cmd/migrate-images/` 脚本
- `migration_progress` 表 migration
- 各站点调用方代码改造（分 PR 推进）：
  - kungal PR-1：业务库加 `*_image_hash` 字段 + GORM model
  - kungal PR-2：上传逻辑改调图片服务
  - kungal PR-3：读取逻辑改用 hash
  - moyu 同上
  - galgame wiki 同上
- CDN rewrite 规则（在外部 Nginx / Cloudflare Workers 配置）
- 离线迁移执行（分站点、分类型）

### 验收标准（每站点独立）

- [ ] 新上传全部走新服务（旧 bucket 上传 QPS 归零）
- [ ] 旧图批量迁移完成，新路径可访问
- [ ] 前端读取优先新 URL，回退旧 URL 兜底
- [ ] 旧 URL 访问日志观察 2 周，降到 < 5% 后可进一步下线兼容层

---

## 配置与部署清单

### 环境变量新增

```env
# Image Service
KUN_IMAGE_SERVICE_HOST=127.0.0.1
KUN_IMAGE_SERVICE_PORT=9278
KUN_IMAGE_PUBLIC_BASE_URL=https://cdn.example.com/img

# Images DB
KUN_IMAGES_PG_HOST=localhost
KUN_IMAGES_PG_PORT=5432
KUN_IMAGES_PG_USER=postgres
KUN_IMAGES_PG_PASSWORD=...
KUN_IMAGES_PG_DATABASE=kun_images

# Object Storage (S3-compatible)
KUN_IMAGE_S3_ENDPOINT=http://127.0.0.1:9000
KUN_IMAGE_S3_REGION=auto
KUN_IMAGE_S3_BUCKET=kun-images-dev
KUN_IMAGE_S3_ACCESS_KEY=...
KUN_IMAGE_S3_SECRET_KEY=...
KUN_IMAGE_S3_FORCE_PATH_STYLE=true       # MinIO 必需

# imgproxy
KUN_IMGPROXY_BASE_URL=https://img.cdn.example.com
KUN_IMGPROXY_KEY=<hex>
KUN_IMGPROXY_SALT=<hex>

# Moderation
KUN_MODERATION_PROVIDER=aliyun           # aliyun / tencent / noop
KUN_MODERATION_ALIYUN_ACCESS_KEY=...
KUN_MODERATION_ALIYUN_SECRET_KEY=...
KUN_MODERATION_SYNC_TIMEOUT_MS=300
KUN_MODERATION_ASYNC_WORKERS=4
```

### 本地开发依赖

- Docker Compose 新增 services：
  - `minio`（对象存储）
  - `imgproxy`
  - （可选）`redis`（配额计数）

- 系统依赖：
  - libvips：`sudo pacman -S libvips`（Arch）/ `apt install libvips-dev`（Debian）

### CI 补充

- Lint + Vet（已有）
- 单元测试（`go test ./internal/platform/image/...`）
- 集成测试：TestMain 启动 MinIO + imgproxy 容器，跑端到端

---

## 风险登记

| 风险 | 里程碑 | 缓解 |
|------|--------|------|
| libvips CGO 构建失败 | M1 | 准备 `kolesa-team/go-webp` 作为纯 CGO 备选；CI 镜像固化依赖 |
| MinIO / S3 协议兼容性差异 | M1 | 使用 `aws-sdk-go-v2` 泛协议，用 MinIO 开发 + 生产切 R2 测试 |
| 审核 API 额度或审核误杀 | M4 | 先 noop provider 跑通链路；上线后先走 shadow mode（记录不拦） |
| 迁移脚本对业务库的写入冲突 | M5 | 业务库加 `*_image_hash` 字段时用 default NULL，不阻塞旧读；迁移脚本 UPDATE 时 WHERE 空值保护 |
| 图片服务成为单点 | 全程 | 单实例 OK（无状态），流量大了水平扩；DB 用现有 PG HA |

---

## 上线后 KPI

- **可用性**：`/image/upload` 成功率 > 99.9%
- **性能**：P99 上传处理耗时 < 800ms（含审核 300ms）
- **去重率**：`deduplicated_count / upload_count` > 10%（跨用户头像重复、meme 图等）
- **审核准确率**：人工抽检 200 张，误杀 < 2%，漏杀 < 5%
- **成本**：对象存储月成本 < 旧系统 × 1.2（去重省 + 冷存储省）

---

## 文档维护

本目录文档与代码同仓库演进：

- 设计变更 → 先改本目录文档再提 PR 实现
- 新的非平凡决策 → 追加到 `01-design.md` 的"核心设计决策"或新建 `06-xxx.md`
- API 变更 → 同步改 `03-api-design.md` 与 `docs/integration/image_service/api-reference.md`（M3 阶段生成 integration 文档）

参考 `docs/galgame_wiki/` 的维护方式。
