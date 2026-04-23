# 01 — 设计原理

## 背景

目前 kungal、moyu、galgame wiki 三个站点各自独立处理图片上传，存在以下问题：

1. **路径格式不统一**
   ```
   kungal:       topic/user_${uid}/${userName}-${unixMS}.webp
   kungal:       avatar/user_${uid}/avatar{,-100}.webp          ← 固定文件名、就地覆盖
   galgame wiki: galgame/${gid}/banner/banner{,-mini}.webp      ← 同上
   ```
2. **压缩/审核/鉴权逻辑各写一份**，三家代码不同、bug 重复修复
3. **无去重**，同一张图被不同用户传多次 = 多份存储
4. **无审核**，违规图进来只能靠人肉发现
5. **无法横向扩展新站点**，加第 4 个站要再抄一遍

随着未来还会有更多站点接入、galgame wiki 需要保留高清原图、以及接入 AI 审核的需求，集中式图片服务是必然选择。

## 目标

| 目标 | 说明 |
|------|------|
| **统一接口** | 所有站点通过同一个 HTTP API 上传图片 |
| **统一处理** | 格式转换（→ WebP）、尺寸压缩、EXIF 剥离、MIME 嗅探集中一处 |
| **内容去重** | 内容寻址存储，同 hash 只存一份 |
| **按需派生** | 缩略图/中图/大图通过 imgproxy 实时生成，CDN 缓存结果 |
| **分层审核** | 同步拦明确违规，异步深度审核 |
| **多站扩展** | 新站点接入 = 注册一个 OAuth Client + 加配置，不改代码 |
| **旧数据平滑迁移** | 旧 URL 不断链，业务代码渐进切换 |

## 非目标

- ❌ 不做通用文件服务（仅图片）
- ❌ 不做图片编辑器（旋转、滤镜、水印等）
- ❌ 不提供面向最终用户的公开上传端点

## 整体架构

```
┌───────────────┐  ┌───────────────┐  ┌────────────────────┐
│ kungal (web)  │  │ moyu (web)    │  │ galgame wiki (web) │
└───────┬───────┘  └───────┬───────┘  └──────────┬─────────┘
        │ JWT              │ JWT                 │ JWT
        │ (image:upload)   │                     │
        ▼                  ▼                     ▼
┌──────────────────────────────────────────────────────────┐
│                    Image Service (Fiber)                  │
│   ┌──────────┐  ┌────────────┐  ┌──────────┐  ┌────────┐ │
│   │ Upload   │→ │ Processor  │→ │ Storage  │  │ Moder  │ │
│   │ handler  │  │ (libvips/  │  │ (S3 SDK) │  │ -ation │ │
│   │          │  │  go-webp)  │  │          │  │  hook  │ │
│   └──────────┘  └────────────┘  └──────────┘  └────┬───┘ │
│         │                                           │     │
│         ▼                                           ▼     │
│   ┌────────────┐                             ┌──────────┐ │
│   │ images DB  │                             │  Queue   │ │
│   │ (Postgres) │                             │(goroutine│ │
│   └────────────┘                             │ or Redis)│ │
│                                              └────┬─────┘ │
└──────────────────────────────────────────────────┼────────┘
                                                    │ async
                        ┌───────────────────────────▼──────┐
                        │  AI Moderation Worker            │
                        │  (调用云厂商 or 本地模型)         │
                        └──────────────────────────────────┘

                     ┌───────────────────────────────┐
                     │   对象存储 (S3 / R2 / OSS)    │
                     │   Bucket: kun-images          │
                     └──────────────┬────────────────┘
                                    │ origin
                                    ▼
                           ┌─────────────────┐
                           │   imgproxy      │ ← 按需变体（resize/format）
                           └────────┬────────┘
                                    │
                                    ▼
                           ┌─────────────────┐
                           │      CDN        │ ← 边缘缓存变体
                           └────────┬────────┘
                                    │
                              终端用户浏览器
```

## 核心设计决策

### 决策 1：鉴权复用 OAuth，不引入新凭证

**选择**：不新增 API Key / 上传 ticket，沿用本仓库已有的 OAuth 基础设施。

- **后端调用**：各站点后端作为 OAuth Client（kungal / moyu 已注册），走 Client Credentials 拿访问令牌，带 `image:upload` scope
- **前端调用**：用户登录拿到的 JWT（`aud` 字段标明来源站）即可用于前端直传，图片服务校验 scope + aud

**理由**：
- 少一套凭证体系 = 少一套过期/泄露/吊销路径
- `oauth_client` 表已经是"站点"的天然 registry，加几个字段就能承载图片服务的配额/开关配置
- 审计日志可以和 OAuth 访问日志合流

**反面观点**：OAuth Client Credentials 的 access_token 有效期通常较短（1h），每次上传要先换 token。对内网后端调用略啰嗦，但可以在调用方做 token 缓存层解决。

### 决策 2：图片服务 DB 不存业务引用关系

**选择**：`images` 表只存 "hash + storage_key + metadata + 审核状态"，**不存** "哪个站的哪个实体在用这张图"。业务引用（`users.avatar_image_hash`、`galgame.banner_image_hash`）由各调用方自己的库维护。

**理由**：
- 每新增一类实体（avatar / banner / topic / cover / post_attachment...）都要改图片服务的 schema 或业务约定，违反单一职责
- 跨服务维护引用计数天然竞态（A 上传 X 的同时 B 独立上传 X → 引用计数临时归零被清理 worker 吃掉）
- 调用方查"用户 X 的当前头像"应该是本地 JOIN，不是跨服务 RPC

**权衡**：
- ✅ 图片服务 100% 通用，未来接第 N 个站零适配
- ✅ 避免跨服务一致性问题
- ❌ 图片服务无法回答"这张图被哪些地方用了"——要靠各业务库聚合
- ❌ 孤儿图清理不能靠精确引用计数，改用 **"TTL + last_referenced_at ping"** 软机制（见决策 4）

### 决策 3：按需派生（imgproxy）而非上传时预生成

**选择**：上传时仅存一份（原图或压缩到 1920×1080 的 WebP，按站点配置），派生尺寸通过 imgproxy 实时生成 + CDN 缓存。

**理由**：

| 维度 | 预生成（旧方案） | imgproxy 按需 |
|------|----------------|---------------|
| 加新 preset | 全量重跑 | 改 URL 即可 |
| 冷门变体 | 白算白存 | 不算不存 |
| 存储成本 | 3–5× | 1× |
| 首访延迟 | 0 | +50–200ms（之后 CDN 命中） |
| 复杂度 | 自己写流水线 | 多部署一个容器 |

以当前几千张/天的规模，imgproxy 零压力；未来扩到图片站的高峰也能水平扩。

**安全**：imgproxy URL 参数必须 HMAC 签名，防止有人让你服务器生成 100 万种尺寸打爆带宽/CPU。

### 决策 4：软清理（TTL + ping），不用引用计数

**选择**：`images` 表有一列 `last_referenced_at`，调用方周期性（例如每天）批量 ping 一次"我现在还在引用这些 hash"。图片服务的清理 worker：

- `last_referenced_at > now - 60d` → 保持在热存储
- `last_referenced_at > now - 180d` → 转冷存储（S3 IA / Glacier）
- `last_referenced_at > now - 365d` → 软删（打标），再 30 天后物理删除

**理由**：
- 避免跨服务原子引用计数的并发问题
- 调用方漏 ping 最坏浪费一点冷存储，不丢数据
- 新接入的站点不用实现"上传 / 引用 / 解引用"完整生命周期，只需在"当前还在用的图"里周期性批量 ping

### 决策 5：分层审核（同步 + 异步）

**选择**：
- **同步层**：上传请求返回前用轻量模型（本地 NSFW small model 或云厂商快速 API <200ms）拦截高置信度违规（裸露/血腥/政治敏感），命中直接 4xx
- **异步层**：上传成功后入队，深度审核（OCR 识别违规文字、商标、版权等）结果异步回填 `review_status`
- **展示侧**：调用方拿图前对比 `review_status`，未过审的走占位图

**理由**：
- 纯异步体验差：用户发完帖刷新，自己的图显示"审核中"
- 纯同步拖延迟、成本高：每张图都跑重模型太贵

**注意**：审核失败不删文件，只打标 + 加黑名单 + 返回占位图。保留证据用于申诉/监管。

### 决策 6：路径格式——单一 content-addressed

**选择**：所有图片统一物理存储路径：

```
/<site>/<hash[:2]>/<hash[2:4]>/<hash>.<ext>
```

**例子**：
```
kungal/ab/cd/abcd1234...ef.webp
moyu/12/34/1234abcd...ff.webp
galgame_wiki/56/78/5678...aa.jpg
```

不做 conv.md 里最初讨论的 `/c/` vs `/e/` 分离。因为"实体关联"语义（用户当前头像、galgame 当前 banner）由调用方库里 `users.avatar_image_hash` 字段表达即可，**不需要**在 URL 层反映。

调用方渲染头像时：
```sql
SELECT i.hash FROM users u JOIN images i ON i.hash = u.avatar_image_hash WHERE u.id = 123
```
拿到 hash 后拼成图片服务的 URL 即可。换头像只是改一个外键，图片服务无感。

## 技术栈

| 层 | 选型 | 备注 |
|---|------|------|
| HTTP | Fiber v3 | 复用现有 |
| 图像编解码 | `libvips` (via `davidbyttow/govips`) | 比纯 Go 快 5–10 倍；CGO |
| 备选 | `kolesa-team/go-webp` + `disintegration/imaging` | 也够用，依赖 libwebp |
| MIME 嗅探 | `gabriel-vasile/mimetype` | 不信任 Content-Type |
| 对象存储 | `aws-sdk-go-v2` | S3 协议兼容 R2/OSS/COS/MinIO |
| DB | Postgres（复用现有） | 新增 `images` 表 |
| 审核 | 云厂商 API（阿里/腾讯内容安全） | 自建 NSFW 模型作为备选 |
| 变体 | `imgproxy`（独立容器） | URL HMAC 签名 |
| 队列 | goroutine + Postgres/Redis 持久化 | 量小不用 MQ |

## 风险与缓解

| 风险 | 可能性 | 缓解 |
|------|--------|------|
| libvips 内存解压炸弹 | 中 | 限制解码后像素总数（如 50MP），限制原文件大小 |
| 审核 API 故障 | 中 | 同步审核挂时降级为"标记为待审核"，异步审核挂时重试队列 |
| imgproxy URL 被刷 | 低 | HMAC 签名 + 有效期 + CDN 层限流 |
| 对象存储 Region 故障 | 低 | 跨区域复制（M5 之后考虑） |
| OAuth 令牌换发瓶颈 | 低 | 调用方缓存 access_token；Client Credentials 长 TTL |

## 与本仓库现有模块的关系

- **复用**：`pkg/config`、`internal/infrastructure/database`、`internal/infrastructure/oauth`、中间件（CORS、JWT、限流）
- **新增**：`internal/platform/image/`（参照 `internal/platform/galgame/` 的分层）
- **扩展**：`oauth_client` 表追加 `image_quota_daily`、`image_allow_original`、`image_allowed_presets` 字段（见 [02-storage-and-schema.md](./02-storage-and-schema.md)）
- **新 cmd**：
  - `cmd/image/` — 独立启动的图片服务进程（端口 `:9278`，与 galgame wiki `:9280` 平级）
  - `cmd/migrate-images/` — 旧系统迁移脚本
  - `cmd/image-gc/` — 软清理 worker

下一篇：[02 — 存储与 Schema 设计](./02-storage-and-schema.md)
