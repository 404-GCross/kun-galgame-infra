# Artifact 服务 — GET API 清单

> **更正（2026-06）**：moderation 骨架已删除（doc 18 P0，T&S 平台改由 cmd/trust 承载）；artifact **有独立服务与路由**（`cmd/artifact`）；下文「未实现」为旧表述。


> 服务: **artifact**（`apps/api/cmd/artifact`） · Base URL: `/api/v1`
>
> 路由源: `apps/api/cmd/artifact/main.go`
>
> 配套: [oauth.get.md](./oauth.get.md) · [image.get.md](./image.get.md) · [galgame.get.md](./galgame.get.md) · [moderation.get.md](./moderation.get.md)
>
> **未审计** —— artifact 有独立 cmd（`cmd/artifact`，未接入 oauth 主进程），本轮字段审计未覆盖，状态 待审计；moderation 骨架已删除（doc 18 P0）。

## 图例 — 审计状态

- 已审计无问题 · 已修 · 保持（有意保持当前行为） · 待审计

## 图例 — 鉴权

| 标记 | 中间件 | 含义 |
|---|---|---|
| 公开 | （无） | 公开 / 运维 |
| 登录 | `JWTAuth` | 必须登录（仅验签）|

## 统计

- 本服务 GET 端点：**4**（运维 1 · 制品 3）

---

## 0. 运维

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /healthz` | 公开 | inline | 待审计 | 健康检查(**非** `/api/v1/health`——与 oauth/image 一致挂在根) |

## 1. 制品（artifacts，登录）

| 路径 | 鉴权 | Handler | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/artifacts` | 登录 | `artifactH.List` | 待审计 | 列表 |
| `GET /api/v1/artifacts/:id` | 登录 | `artifactH.Get` | 待审计 | 单个制品元数据 |
| `GET /api/v1/artifacts/:id/download` | 登录 | `artifactH.Download` | 待审计 | 下载 |
