# Image 服务 — POST API 清单

> 服务: **image**（`apps/api/cmd/image`） · Base URL: `/`（无 `/api` 前缀）· 路由源: `cmd/image/main.go`
>
> 图例见 [README](./README.md)。配套: [image.get.md](./image.get.md) · [image.delete.md](./image.delete.md) · [image.patch.md](./image.patch.md)
>
> **审计完成** —— 🔧 已修 / ✅ 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 统计

- 本服务 POST 端点：**2**（均为服务到服务，ClientAuth）

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /image/upload` | 🔑 | `h.Upload` | 🔧 | 上传（multipart：file + preset）；未配置上传时为 `uploadDisabled` 占位；#17 复活软删 hash(避免 UNIQUE 500)；#33 并发去重 OnConflict 收敛 |
| `POST /image/reference-ping` | 🔑 | `h.Ping` | ✅ | 刷新 `last_referenced_at`（GC 保活）|
