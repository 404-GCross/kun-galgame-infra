# Image 服务 — POST API 清单

> 服务: **image**（`apps/api/cmd/image`） · Base URL: `/`（无 `/api` 前缀）· 路由源: `cmd/image/main.go`
>
> 图例见 [README](./README.md)。配套: [image.get.md](./image.get.md) · [image.delete.md](./image.delete.md) · [image.patch.md](./image.patch.md)
>
> **inventory 阶段** —— 状态列 ⏳。

## 统计

- 本服务 POST 端点：**2**（均为服务到服务，ClientAuth）

---

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `POST /image/upload` | 🔑 | `h.Upload` | ⏳ | 上传（multipart：file + preset）；未配置上传时为 `uploadDisabled` 占位 |
| `POST /image/reference-ping` | 🔑 | `h.Ping` | ⏳ | 刷新 `last_referenced_at`（GC 保活）|
