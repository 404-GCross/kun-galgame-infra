# 03 — API 设计

## 基础约定

- **Base URL**：`https://image.api.example.com`（生产）/ `http://127.0.0.1:9278`（开发）
- **Content-Type**：
  - 上传：`multipart/form-data`
  - 其他：`application/json`
- **鉴权**：`Authorization: Bearer <access_token>`
  - 后端调用：OAuth Client Credentials 换的 access_token
  - 前端调用：用户登录的 JWT（`aud` 字段必须匹配已注册站点）
- **Scope 要求**：
  - 上传：`image:upload`
  - 元信息查询：`image:read`（公开图默认允许，私有图需要）
  - 删除/管理：`image:admin`

## 错误响应

统一格式：

```json
{
  "error": {
    "code": "quota_exceeded",
    "message": "daily upload quota exceeded: 10000/10000",
    "details": {
      "quota": 10000,
      "used": 10000,
      "reset_at": "2026-04-24T00:00:00Z"
    }
  }
}
```

| HTTP | code | 场景 |
|------|------|------|
| 400 | `invalid_file` | MIME 嗅探失败 / 损坏的文件 / 不支持的格式 |
| 400 | `invalid_preset` | 站点未开通此 preset |
| 401 | `unauthorized` | 缺失或无效 token |
| 403 | `scope_missing` | token 缺少必要 scope |
| 403 | `site_disabled` | 站点未开启图片服务 |
| 413 | `file_too_large` | 超过站点上限 |
| 422 | `rejected_moderation` | 同步审核拦截 |
| 429 | `quota_exceeded` | 超出站点日配额 |
| 429 | `rate_limited` | 超过瞬时速率限制 |
| 500 | `internal_error` | 服务异常 |

---

## 1. 上传图片

### `POST /image/upload`

上传一张图片，同步处理并返回永久 URL。

**请求**：

```http
POST /image/upload HTTP/1.1
Host: image.api.example.com
Authorization: Bearer <token>
Content-Type: multipart/form-data; boundary=----xxx

------xxx
Content-Disposition: form-data; name="file"; filename="photo.png"
Content-Type: image/png

<binary data>
------xxx
Content-Disposition: form-data; name="preset"

cover
------xxx--
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `file` | file | ✅ | 图片二进制，支持 `image/jpeg` `image/png` `image/webp` `image/gif`（首帧） |
| `preset` | string | ✅ | 处理预设名，必须在本站 `image_allowed_presets` 中 |
| `keep_original` | bool | | 是否保留原图（需站点开启 `image_allow_original`） |

**成功响应**：

```json
{
  "hash": "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234567890ef",
  "url": "https://cdn.example.com/img/kungal/ab/cd/abcd...ef.webp",
  "width": 1920,
  "height": 1080,
  "size_bytes": 245678,
  "mime": "image/webp",
  "ext": "webp",
  "review_status": "approved",
  "deduplicated": false,
  "variants": {
    "thumbnail": "https://img.cdn/.../rs:fit:400:400/...",
    "avatar": "https://img.cdn/.../rs:fit:256:256/..."
  }
}
```

**字段说明**：
- `hash` — 内容 hash，调用方存入自己库（`users.avatar_image_hash`）作为外键
- `url` — 可直接用于 `<img src>` 的 CDN 永久 URL
- `review_status` — `approved` / `pending`（异步审核中）/ `rejected`（同步拦截不会到这里，会返回 422）
- `deduplicated` — 是否命中已存在图（调用方可用于统计）
- `variants` — 当前 preset 之外的备选变体 URL（imgproxy 签名后给出，便于直接使用）

**调用方接入例子（TypeScript 前端）**：

```ts
const fd = new FormData()
fd.append('file', file)
fd.append('preset', 'avatar')

const res = await fetch('https://image.api.example.com/image/upload', {
  method: 'POST',
  headers: { Authorization: `Bearer ${userJWT}` },
  body: fd
})
const { hash, url } = await res.json()
await api.patch('/users/me', { avatar_image_hash: hash })
```

---

## 2. 查询元信息

### `GET /image/:hash`

查询一张图片的元信息。

**请求**：
```http
GET /image/abcd1234...ef HTTP/1.1
Authorization: Bearer <token>
```

**响应**：
```json
{
  "hash": "abcd...ef",
  "url": "https://cdn.example.com/img/kungal/ab/cd/abcd...ef.webp",
  "site": "kungal",
  "width": 1920,
  "height": 1080,
  "size_bytes": 245678,
  "mime": "image/webp",
  "review_status": "approved",
  "review_labels": {
    "nsfw": 0.02,
    "violence": 0.01
  },
  "created_at": "2026-04-23T10:20:30Z"
}
```

**404** 如果 hash 不存在或已物理删除。

**403** 如果 token 所属站与图片 `site` 不符，且缺少 `image:admin` scope（防止跨站探测）。

---

## 3. 批量续期引用

### `POST /image/reference-ping`

调用方周期性上报"我当前还在引用这些图"，图片服务更新其 `last_referenced_at`。

**请求**：
```json
{
  "hashes": [
    "abcd...ef",
    "1234...aa",
    "5678...bb"
  ]
}
```

最多 1000 个 hash / 请求。

**响应**：
```json
{
  "updated": 998,
  "not_found": ["5678...bb"],
  "unauthorized": []
}
```

- `not_found` — 本站未上传过这些 hash（可能已被清理）
- `unauthorized` — 非本站的 hash（如果传了的话）

**建议频率**：每天一次即可。新引用产生时无需立即 ping（上传本身已刷新 `last_referenced_at`）。

---

## 4. 构造变体 URL（非 HTTP 接口，SDK 函数）

派生尺寸不走后端 API，由调用方自行拼 URL。图片服务提供 SDK 工具函数：

**Go SDK**：
```go
import "api/pkg/imageclient"

url := imageclient.BuildVariant(hash, site, ext, imageclient.VariantOpts{
    Width:   400,
    Height:  400,
    Format:  "webp",
    Preset:  "thumbnail",  // 或直接指定 w/h
})
```

**TypeScript SDK**（前端也需要）：
```ts
import { buildImageVariant } from '@kun/image-client'

const url = buildImageVariant(hash, { preset: 'thumbnail' })
```

SDK 内部用预配置的 HMAC key 签名 URL 路径，返回可直接使用的 imgproxy URL。签名 key 通过构建时环境变量注入，前端 SDK 的 key 是"只读签名"专用的 weak key，不等同后端签名 key（见运维文档）。

---

## 5. 管理端点（后台）

以下接口需要 `image:admin` scope，仅供管理员/审核员使用。

### `GET /admin/image/list`

**查询参数**：
- `site` — 过滤站点
- `review_status` — `pending` / `rejected` / `manual_review`
- `uploader_sub` — 按上传者过滤
- `from` / `to` — 时间范围
- `page` / `limit`

**响应**：分页列表，字段同单个 `GET /image/:hash`。

### `PATCH /admin/image/:hash/review`

手动调整审核状态。

**请求**：
```json
{
  "status": "approved" | "rejected" | "manual_review",
  "reason": "误杀，人工放行"
}
```

### `DELETE /admin/image/:hash`

软删（打 `deleted_at`），30 天后物理删除。

**请求参数**：
- `force=true` — 立即物理删除（仅超管，记录审计）

### `GET /admin/stats`

**查询参数**：
- `site`
- `from` / `to`

**响应**：
```json
{
  "upload_count": 12345,
  "unique_images": 10234,
  "deduplicated_count": 2111,
  "total_bytes": 123456789012,
  "rejected_count": 45,
  "by_preset": {
    "cover": 8000,
    "thumbnail": 3000,
    "avatar": 1345
  }
}
```

---

## 6. 健康检查

### `GET /healthz`

```json
{
  "status": "ok",
  "postgres": "ok",
  "storage": "ok",
  "imgproxy": "ok",
  "moderation": "ok"
}
```

任意依赖不健康则 HTTP 503。

### `GET /metrics`

Prometheus 指标端点（标准 Go runtime + 自定义业务指标）。

自定义指标：
- `image_upload_total{site,result}` — 按结果计数（success / rejected_xxx）
- `image_upload_duration_seconds{site,preset}` — 上传处理延迟直方图
- `image_processing_duration_seconds{op}` — 按处理阶段（decode / resize / encode / store）
- `image_storage_bytes{site}` — 总存储字节数（周期采样）
- `image_moderation_duration_seconds` — 审核延迟
- `image_dedup_hits_total{site}` — 去重命中数

---

## 端点汇总

| Method | Path | Scope | 说明 |
|--------|------|-------|------|
| POST | `/image/upload` | `image:upload` | 上传图片 |
| GET | `/image/:hash` | `image:read` | 查询元信息 |
| POST | `/image/reference-ping` | `image:upload` | 批量续期引用 |
| GET | `/admin/image/list` | `image:admin` | 管理：列表 |
| PATCH | `/admin/image/:hash/review` | `image:admin` | 管理：修改审核状态 |
| DELETE | `/admin/image/:hash` | `image:admin` | 管理：软删/硬删 |
| GET | `/admin/stats` | `image:admin` | 管理：统计 |
| GET | `/healthz` | — | 健康检查 |
| GET | `/metrics` | — | Prometheus 指标 |

## CORS

仅对前端直传场景开放。白名单从 `oauth_client.redirect_uris` 派生（各站点注册时填过），外加显式配置的 `image_cors_origins` 字段（可选）。

Preflight 放行 `Authorization` `Content-Type` `X-Requested-With`。

下一篇：[04 — 迁移计划](./04-migration-plan.md)
