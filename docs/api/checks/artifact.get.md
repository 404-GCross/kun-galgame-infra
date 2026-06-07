# Artifact 服务 — GET API 清单

> ⚠️ **更正（2026-06）**：moderation / artifact **已有独立服务与路由**（`cmd/moderation` / `cmd/artifact`）；下文「未实现」为旧表述，仅指本轮未做字段对齐审计。


> 服务: **artifact**（`apps/api/cmd/artifact`） · Base URL: `/api/v1`
>
> 路由源: `apps/api/cmd/artifact/main.go`
>
> 配套: [oauth.get.md](./oauth.get.md) · [image.get.md](./image.get.md) · [galgame.get.md](./galgame.get.md) · [moderation.get.md](./moderation.get.md)
>
> **未审计** —— 该服务已有独立 cmd（`cmd/moderation` / `cmd/artifact`，未接入 oauth 主进程），本轮字段审计未覆盖，状态 ⏳。

## 图例 — 审计状态

- ✅ 已审计无问题 · 🔧 已修 · ⏭️ 有意保持 · ⏳ 待审计

## 图例 — 鉴权

| 标记 | 中间件 | 含义 |
|---|---|---|
| 🌐 | （无） | 公开 / 运维 |
| 🔒 | `JWTAuth` | 必须登录（仅验签）|

## 统计

- 本服务 GET 端点：**4**（运维 1 · 制品 3）

---

## 0. 运维

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/health` | 🌐 | inline | ⏳ | 健康检查 |

## 1. 制品（artifacts，登录）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/artifacts` | 🔒 | `artifactH.List` | ⏳ | 列表 |
| `GET /api/v1/artifacts/:id` | 🔒 | `artifactH.Get` | ⏳ | 单个制品元数据 |
| `GET /api/v1/artifacts/:id/download` | 🔒 | `artifactH.Download` | ⏳ | 下载 |
