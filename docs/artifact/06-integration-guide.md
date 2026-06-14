# 06 — 接入指南（调用方视角）

> 面向要接入大文件上传/下载的下游站点（kungal / moyu / touchgal / wiki 等）。术语与端点见 [03-api-design](./03-api-design.md)。

## 0. 前置：注册 OAuth Client

artifact 不引入新凭证，沿用 OAuth Client 作「站点」。给你的站点 client 配：

| 字段 | 值 |
|------|----|
| `artifact_enabled` | `true` |
| `artifact_site_key` | 站点标识，如 `touchgal`（= 对象 key 前缀 + 元数据 site）|
| `artifact_quota_daily` / `artifact_quota_bytes_daily` | 每日文件数 / 字节数上限 |
| `artifact_max_file_size` | 单文件上限 |
| `artifact_allowed_mime` | 可选白名单，如 `[".zip",".7z",".rar",".iso"]`（MIME 或扩展）|
| `artifact_cdn_base` | 可选，站点 CF Worker 下载域名（[04](./04-cloudflare-worker.md)）|
| scope | 前端直传需在 `allowed_scopes` 含 `artifact:upload` |

## 1. 两种调用方式

| | 后端 S2S（Basic）| 前端直传（JWT）|
|--|--|--|
| `Authorization` | `Basic base64(client_id:secret)` | `Bearer <用户 JWT>` |
| 额外头 | — | `X-Kun-Artifact-Client-Id: <client_id>` |
| 要求 | client `artifact_enabled` | JWT 含 `artifact:upload` scope 且 site 匹配 |
| 适用 | 服务器代用户上传 / 后台任务 | 浏览器直接发起（推荐，省一跳带宽）|

> 无论哪种，**文件字节都直传 B2**，不经过 artifact 服务，也不经过你的后端（前端直传时）。

## 2. 上传三步舞

### 2.1 单文件（`<50MB`）

```ts
// ① init
const init = await api('/api/v1/artifacts', {
  method: 'POST',
  body: { name: file.name, file_size: file.size, mime_type: file.type, checksum, public: false },
}) // → { uuid, multipart:false, upload_url, expires_at }

// ② 直传 B2
await fetch(init.upload_url, { method: 'PUT', body: file })

// ③ complete
const art = await api(`/api/v1/artifacts/${init.uuid}/complete`, { method: 'POST', body: {} })
// → ArtifactResponse { uuid, status:1, ... }
```

### 2.2 大文件分片（`≥50MB`）

```ts
// ① init → { uuid, multipart:true, upload_id, part_size, part_urls:[{part_number,url}] }
const init = await api('/api/v1/artifacts', {
  method: 'POST',
  body: { name: file.name, file_size: file.size, mime_type: file.type },
})

// ② 切片并发 PUT，收集每片 ETag
const parts = await Promise.all(init.part_urls.map(async (p) => {
  const start = (p.part_number - 1) * init.part_size
  const blob = file.slice(start, start + init.part_size)        // 最后一片自然更小
  const res = await fetch(p.url, { method: 'PUT', body: blob })
  return { part_number: p.part_number, etag: res.headers.get('ETag')! }
}))

// ③ complete（带 parts，可选 manifest）
const art = await api(`/api/v1/artifacts/${init.uuid}/complete`, {
  method: 'POST',
  body: { parts, manifest: { executable: 'game.exe' } },
})
```

> **可选 sha256**：大文件可用 Web Crypto 流式算 `checksum` 传给 init（存元数据备查）。v1 服务端不复算，但传了对将来开启校验有益。

## 3. B2 桶 CORS（前端直传必读）

浏览器直接 `PUT` 到 B2、且 multipart 需读取 `ETag` 响应头——这要求 **B2 桶的 CORS 规则**：

- 允许你的前端 origin 的 `PUT`（和单文件的 `GET`）。
- **`exposeHeaders` 必须含 `ETag`**，否则 JS 读不到分片 ETag，complete 会失败。

（后端 S2S 路径不受 CORS 限制。）这是 B2 桶级配置，由运维设，不在 artifact 服务侧。

## 4. 下载

```ts
const { url, expires_at } = await api(`/api/v1/artifacts/${uuid}/download`, { method: 'GET' })
// 私有：presigned GET(~1h，带 Content-Disposition)；public+Worker：缓存域名 URL
window.location.href = url     // 或 <a href={url} download>
```

下载也走鉴权（同一 client）。私有内容请保持 `public=false`（每次签发、短时效）；只有真正公开、希望走 CF 缓存省流量的才设 `public=true` 且站点配 `artifact_cdn_base`。

## 5. 删除 / 查询

```ts
await api(`/api/v1/artifacts/${uuid}`, { method: 'DELETE' })   // 软删，GC 到期物删
await api(`/api/v1/artifacts/${uuid}`, { method: 'GET' })       // 元数据
await api(`/api/v1/artifacts?page=1&page_size=20`, { method: 'GET' }) // 本站列表 {items,total}
```

把 `uuid` 存进你自己的业务库（如 `patch.artifact_uuid`）作外键；artifact 服务不知道你的业务实体。

## 6. 错误处理

按**整数 `code`** 分类（见 [03 错误响应](./03-api-design.md#错误响应)）。常见：

| code | 含义 | 调用方动作 |
|------|------|-----------|
| 50004 | 超单文件上限 | 提示用户文件过大 |
| 50012 | 超日配额 | 提示稍后 / 提额 |
| 50015 | 完成时大小不符 | 重新 init 重传 |
| 50017 | 文件类型不允许 | 提示支持的类型 |
| 50014 | 上传未开放 | 功能灰度中 |
| 401/403 | 鉴权 / 站点配置 | 检查 client / scope / `artifact_enabled` |

## 7. 注意事项

- **预签名有效期**：init 返回的 URL ~1h 过期；大文件分片要在窗口内传完，否则重新 init（旧的成孤儿，GC 回收）。
- **重传**：分片失败可只重 PUT 失败的那片（同 `part_url` 在有效期内可重试）；整体失败就重新 init。
- **幂等**：对已 `ready` 的 uuid 再 complete 是安全的（只会补 manifest）。
- **孤儿**：init 后不 complete 的会被 `artifact-gc` 在 `ORPHAN_TTL`（默认 24h）后清掉，无需调用方干预。
- **跨站隔离**：你只能看/操作自己 `site_key` 的制品；别站 uuid 一律 404。

---

← 返回 [README 索引](./README.md)
