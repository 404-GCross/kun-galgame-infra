# Image 服务 — GET API 清单

> 服务: **image**（`apps/api/cmd/image`）+ 管理端（跑在 oauth 进程内）
>
> 路由源:
> - 公开/服务端：`apps/api/cmd/image/main.go`（Base URL: `/`，无 `/api` 前缀）
> - 管理端：`apps/api/cmd/oauth/main.go` 的 `registerImageAdmin`（Base URL: `/api/v1/admin/image`，**跑在 oauth 进程**，因 admin 鉴权在那边）
>
> 配套: [oauth.get.md](./oauth.get.md) · [galgame.get.md](./galgame.get.md) · [moderation.get.md](./moderation.get.md) · [artifact.get.md](./artifact.get.md)
>
> **审计完成** —— 已修 / 已审计无问题（本轮字段对齐/越权/SQL注入/副作用扫描未发现可处理问题）。详见 [README 审计结果](./README.md#审计结果2026-05-29)。

## 图例 — 审计状态

- 已审计无问题 · 已修 · 保持（有意保持当前行为） · 待审计

## 图例 — 鉴权

| 标记 | 中间件 | 含义 |
|---|---|---|
| 公开 | （无） | 公开 / 运维 |
| ClientAuth | `imgMW.ClientAuth` | 服务到服务（OAuth client + 站点配额/feature gate）|
| admin | `Auth` + `RequireRole("admin")` | 仅 admin（管理端，oauth 进程）|

## 统计

- 本服务 GET 端点：**6**
  - 运维 2 · 服务端读取 2 · 管理端 2
- 写操作（POST upload / reference-ping、DELETE :hash、PATCH :hash/review）不在本文件，见对应 method 清单。

---

## 0. 运维（cmd/image）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /healthz` | 公开 | inline | 已审计 | 健康检查 |
| `GET /metrics` | 公开 | `promhttp.Handler` | 已审计 | Prometheus 指标 |

## 1. 服务端读取（cmd/image，ClientAuth）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /image/stats` | ClientAuth | `h.Stats` | 已审计 | 按站点统计 |
| `GET /image/:hash` | ClientAuth | `h.Meta` | 已审计 | 单图元数据（含 sites 审计信息）|

## 2. 管理端（跑在 oauth 进程，`/api/v1/admin/image`，admin）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/admin/image/list` | admin | `imgHandler.Admin.List` | 已修 | 图片列表（管理后台 /images 页）；#34 非法 from/to/review_status 返回 400(不再静默全量) |
| `GET /api/v1/admin/image/stats` | admin | `imgHandler.Admin.Stats` | 已修 | 全局统计；#35 by_site/unique 排除软删 hash，与 total_bytes 口径一致 |
