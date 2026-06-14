# Moderation 服务 — GET API 清单

> **更正（2026-06）**：moderation / artifact **已有独立服务与路由**（`cmd/moderation` / `cmd/artifact`）；下文「未实现」为旧表述，仅指本轮未做字段对齐审计。


> 服务: **moderation**（`apps/api/cmd/moderation`） · Base URL: `/api/v1`
>
> 路由源: `apps/api/cmd/moderation/main.go`
>
> 配套: [oauth.get.md](./oauth.get.md) · [image.get.md](./image.get.md) · [galgame.get.md](./galgame.get.md) · [artifact.get.md](./artifact.get.md)
>
> **未审计** —— 该服务已有独立 cmd（`cmd/moderation` / `cmd/artifact`，未接入 oauth 主进程），本轮字段审计未覆盖，状态 待审计。

## 图例 — 审计状态

- 已审计无问题 · 已修 · 保持（有意保持当前行为） · 待审计

## 图例 — 鉴权

| 标记 | 中间件 | 含义 |
|---|---|---|
| 公开 | （无） | 公开 / 运维 |
| admin/mod | `JWTAuth` + `RequireRole("admin","moderator")` | admin / moderator（仅验签）|

## 统计

- 本服务 GET 端点：**4**（运维 1 · 审核 3）

---

## 0. 运维

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/health` | 公开 | inline | 待审计 | 健康检查 |

## 1. 审核（admin / moderator）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/moderation/jobs` | admin/mod | `moderationH.ListJobs` | 待审计 | 审核任务列表 |
| `GET /api/v1/moderation/jobs/:id` | admin/mod | `moderationH.GetJob` | 待审计 | 单个审核任务 |
| `GET /api/v1/moderation/policies` | admin/mod | `moderationH.ListPolicies` | 待审计 | 审核策略 |
