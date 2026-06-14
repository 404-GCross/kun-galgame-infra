# Image 服务 — DELETE API 清单

> 服务: **image** · 路由源:
> - `cmd/image/main.go`（服务端，Base `/`，ClientAuth）
> - `cmd/oauth/main.go` 的 `registerImageAdmin`（管理端，`/api/v1/admin/image`，**跑在 oauth 进程**，admin）
>
> 图例见 [README](./README.md)。配套: [image.get.md](./image.get.md) · [image.post.md](./image.post.md) · [image.patch.md](./image.patch.md)
>
> **审计完成** —— 已修 / 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 统计

- 本服务 DELETE 端点：**2**（服务端软删 1 · 管理端 1）

---

## 1. 服务端（cmd/image，ClientAuth）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /image/:hash` | ClientAuth | `h.SoftDelete` | 已修 | 软删（站点作用域；GC 在 TTL 后物删）。用于头像匿名化 GC；#03 注入 images DB 句柄修复 nil-panic 500(+ nil 守卫)；已加 DB 测试 |

## 2. 管理端（跑在 oauth 进程，admin）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `DELETE /api/v1/admin/image/:hash` | admin | `imgHandler.Admin.Delete` | 已审计 | 软删（默认）/ `?force=true` 物删（合规右遗忘）|
