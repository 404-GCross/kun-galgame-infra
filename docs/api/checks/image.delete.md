# Image 服务 — DELETE API 清单

> 服务: **image** · 路由源:
> - `cmd/image/main.go`（服务端，Base `/`，ClientAuth）
> - `cmd/oauth/main.go` 的 `registerImageAdmin`（管理端，`/api/v1/admin/image`，**跑在 oauth 进程**，admin）
>
> 图例见 [README](./README.md)。配套: [image.get.md](./image.get.md) · [image.post.md](./image.post.md) · [image.patch.md](./image.patch.md)
>
> **inventory 阶段** —— 状态列 ⏳。

## 统计

- 本服务 DELETE 端点：**2**（服务端软删 1 · 管理端 1）

---

## 1. 服务端（cmd/image，ClientAuth）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /image/:hash` | 🔑 | `h.SoftDelete` | ⏳ | 软删（站点作用域；GC 在 TTL 后物删）。用于头像匿名化 GC |

## 2. 管理端（跑在 oauth 进程，admin）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /api/v1/admin/image/:hash` | ⚙️ | `imgHandler.Admin.Delete` | ⏳ | 软删（默认）/ `?force=true` 物删（合规右遗忘）|
