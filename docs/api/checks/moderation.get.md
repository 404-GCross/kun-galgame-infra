# Moderation 服务 — GET API 清单

> ⚠️ **更正（2026-06）**：moderation / artifact **已有独立服务与路由**（`cmd/moderation` / `cmd/artifact`）；下文「未实现」为旧表述，仅指本轮未做字段对齐审计。


> 服务: **moderation**（`apps/api/cmd/moderation`） · Base URL: `/api/v1`
>
> 路由源: `apps/api/cmd/moderation/main.go`
>
> 配套: [oauth.get.md](./oauth.get.md) · [image.get.md](./image.get.md) · [galgame.get.md](./galgame.get.md) · [artifact.get.md](./artifact.get.md)
>
> **未审计** —— 该服务尚未实现（仅占位），本轮按要求跳过，状态保持 ⏳。

## 图例 — 审计状态

- ✅ 已审计无问题 · 🔧 已修 · ⏭️ 有意保持 · ⏳ 待审计

## 图例 — 鉴权

| 标记 | 中间件 | 含义 |
|---|---|---|
| 🌐 | （无） | 公开 / 运维 |
| 🛡️ | `JWTAuth` + `RequireRole("admin","moderator")` | admin / moderator（仅验签）|

## 统计

- 本服务 GET 端点：**4**（运维 1 · 审核 3）

---

## 0. 运维

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/health` | 🌐 | inline | ⏳ | 健康检查 |

## 1. 审核（admin / moderator）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/moderation/jobs` | 🛡️ | `moderationH.ListJobs` | ⏳ | 审核任务列表 |
| `GET /api/v1/moderation/jobs/:id` | 🛡️ | `moderationH.GetJob` | ⏳ | 单个审核任务 |
| `GET /api/v1/moderation/policies` | 🛡️ | `moderationH.ListPolicies` | ⏳ | 审核策略 |
